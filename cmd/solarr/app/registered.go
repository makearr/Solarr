package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/internal/service/follow"
)

// registerMedia builds the one list of media types in the binary, each wired to what it needs from the process.
// A new type is one more module here; nothing else in this package names a type's scan.
func (p *process) registerMedia() error {
	registry, err := media.NewRegistry(append([]media.Module{p.musicModule(), p.tvModule()}, p.extraMedia...)...)
	if err != nil {
		return err
	}
	p.media = registry
	p.cache.EnableNamingValidation(adapters.NamingValidator(registry))
	return nil
}

// musicModule wires music's sweep: its consumer, the audio files it reads and its housekeeping.
func (p *process) musicModule() music.Module {
	physical := library.NewPhysical(p.library)
	provider := p.musicBrainzProvider()
	desired := library.NewDesired(p.library)
	people := library.NewPeople(p.library)
	attempts := library.NewAttempts(p.library)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS:           p.fsys,
		Physical:     physical,
		Identity:     library.NewIdentity(p.library),
		Unidentified: library.NewUnidentified(p.library),
		Attempts:     &attempts,
		Search:       provider,
		Albums:       provider,
		Fingerprints: p.tool,
		AcoustID:     p.acoustIDLookup(),
		Tracks:       provider,
		Clock:        clock.NewSystem(),
		Logger:       p.logger,
		// One identification threshold for scan and import (2026-09-24).
		Threshold:  func() float64 { return p.cache.Snapshot().ImportAutoCommitConfidence() },
		Desired:    &desired,
		People:     &people,
		Categories: adapters.Categories{Cache: p.cache},
		// The user's own default when they haven't configured one for the
		// category yet -- ensureDefaultQualityProfile seeded it at startup.
		DefaultProfileID: p.defaultQualityProfileID,
	})
	rename := music.RenameConfig{
		Categories: adapters.Categories{Cache: p.cache},
		Roots:      adapters.DrainRoots{Cache: p.cache}, Files: physical,
		Identities: library.NewDesired(p.library), Works: library.NewWorks(p.library),
		MoveJobs: library.NewMoveJobs(p.library), Queue: p.queue, Clock: clock.NewSystem(),
	}
	feed := music.Feed{
		Grab: p.musicGrab(), Indexers: p.indexerConns, WantedByArtist: library.NewHunt(p.library).WantedAlbumsByArtist, Logger: p.logger,
	}
	check := music.CutoffJob{
		Desired: library.NewDesired(p.library), Settings: p.cache, Recycle: p.recycleSuperseded,
		Clock: clock.NewSystem(), Logger: p.logger,
	}
	following := music.Following{
		People: people, Organisations: library.NewOrganisations(p.library), Identity: library.NewIdentity(p.library), Desired: &desired,
		Categories:  adapters.Categories{Cache: p.cache},
		Discography: provider, DefaultProfileID: p.defaultQualityProfileID, Clock: clock.NewSystem(),
		Search: func(ctx context.Context, categoryID, personID int64) error {
			_, err := music.EnqueueSearchArtist(ctx, p.queue, categoryID, personID)
			return err
		},
	}
	searching := music.Searching{Acquire: p.musicGrab(), Indexers: p.indexerConns, Clock: clock.NewSystem()}
	refresh := func(ctx context.Context, workID int64) (bool, error) {
		result, err := music.EnqueueRefreshAlbum(ctx, p.queue, workID)
		return result.Duplicate, err
	}
	passes := media.Passes{
		WorksPass: p.enrichmentPass,
		PeoplePass: func(ctx context.Context, _ bool) (bool, error) {
			result, err := p.queue.Enqueue(ctx, jobs.Request{
				Type: music.RefreshArtistsJobType, ActionKey: string(music.RefreshArtistsJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
			})
			return result.Duplicate, err
		},
		PicturesPass: p.artworkPass,
	}
	realign := func(ctx context.Context, workID int64) error {
		return music.AlignClaimedFiles(ctx, &desired, clock.NewSystem(), workID)
	}
	return music.Module{Registrations: p.musicJobs(), Hunt: p.musicHunt, Refresh: refresh, Realign: realign, Adder: p.musicAdder(), Pictures: p.musicPictures(), Files: p.musicFiles(), Passes: passes, Feed: feed, CutoffJob: check, Following: following, Searching: searching, Rename: &rename, Sweep: scan.Sweep{
		Consumer: consumer,
		Include:  scan.IsAudio,
		Housekeeping: music.NewHousekeeping(music.HousekeepingConfig{
			FS: p.fsys, Categories: adapters.Categories{Cache: p.cache}, Roots: adapters.DrainRoots{Cache: p.cache},
			Affinity: p.cache, Desired: &desired, Subscriptions: library.NewSubscriptions(p.library),
			People: library.NewPeople(p.library), GroupID: lookupGroupID,
		}),
		// Enrichment follows at once, not at the next hourly run: a new library had no tracklists,
		// photos or Wanted for up to an hour (found 2026-09-28).
		OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx); _, _ = p.enrichmentPass(ctx, false) },
	}}
}

// tvModule wires TV's sweep and the job that identifies again the files that waited for review.
func (p *process) tvModule() tv.Module {
	consumer := tv.NewConsumer(tv.ConsumerConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library),
		Service: p.tvService(), Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
		SharedFiles: p.enqueueSharedFiles,
	})
	retry := tv.NewRetryJob(tv.RetryConfig{
		Unidentified: library.NewUnidentified(p.library), Consumer: consumer, FS: p.fsys, Logger: p.logger,
		Libraries: func() []tv.RetryLibrary {
			snapshot := p.cache.Snapshot()
			var libraries []tv.RetryLibrary
			for _, category := range snapshot.Categories() {
				if category.MediaType == tv.MediaType {
					libraries = append(libraries, tv.RetryLibrary{CategoryID: category.ID, Roots: snapshot.RootFoldersForCategory(category.ID)})
				}
			}
			return libraries
		},
	})
	return tv.Module{Registrations: p.tvJobs(), TMDBConfigured: func() bool { return p.cache.Snapshot().TMDBConfigured() },
		Sweep: scan.Sweep{
			Consumer: consumer, Include: tv.IsVideo,
			OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx) },
			Housekeeping: tv.Housekeeping{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library), Logger: p.logger,
			},
		},
		Hunt:     p.tvHunt,
		Adder:    p.seriesCatalog(),
		Pictures: p.seriesCatalog(),
		Renumber: p.seriesCatalog(),
		Files:    p.unidentifiedTV(),
		Passes: media.Passes{
			WorksPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := tv.EnqueueRefresh(ctx, p.queue)
				return result.Duplicate, err
			},
			PicturesPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := tv.EnqueueArtwork(ctx, p.queue)
				return result.Duplicate, err
			},
		},
		// A series is read again at once, as Sonarr's Refresh: it is one work and a handful of requests.
		Refresh: func(ctx context.Context, workID int64) (bool, error) {
			_, libraries, err := library.NewWorks(p.library).Places(ctx, workID)
			if err != nil {
				return false, err
			}
			if len(libraries) == 0 {
				return false, fmt.Errorf("series %d is in no library", workID)
			}
			return false, p.seriesCatalog().Refresh(ctx, libraries[0], workID)
		},
		Feed: tv.Feed{
			Grab: p.tvGrab(), Reader: library.NewSeriesReader(p.library), Indexers: p.tvIndexerConns, Logger: p.logger,
			Libraries: func() []int64 {
				var ids []int64
				for _, category := range p.cache.Snapshot().Categories() {
					if category.MediaType == tv.MediaType {
						ids = append(ids, category.ID)
					}
				}
				return ids
			},
		},
		CutoffJob: tv.CutoffJob{
			Desired: library.NewDesired(p.library), Series: library.NewSeriesReader(p.library), Settings: p.cache,
			Recycle: p.recycleSuperseded, Logger: p.logger,
		},
		Retry: retry,
		ServerFiles: &tv.ServerFilesJob{
			Writer: tv.ServerFiles{
				FS: p.fsys, Reader: library.NewSeriesReader(p.library), Crossovers: library.NewCrossovers(p.library),
				Roots: adapters.Roots{Cache: p.cache}, Picture: p.remoteImages().Original, Clock: clock.NewSystem(),
			},
			Libraries: func() []settings.Category { return p.cache.Snapshot().Categories() },
			RootsOf: func(categoryID int64) []settings.RootFolder {
				return p.cache.Snapshot().RootFoldersForCategory(categoryID)
			},
			Logger: p.logger,
		},
		SharedFiles: &tv.SharedFilesJob{Placer: tv.SharedFiles{
			FS: p.fsys, Crossovers: library.NewCrossovers(p.library), Reader: library.NewSeriesReader(p.library),
			Links: library.NewObjectLinks(p.library), Series: p.tvService().Series, Categories: adapters.Categories{Cache: p.cache},
			Roots: adapters.Roots{Cache: p.cache},
			RootsOf: func(categoryID int64) []settings.RootFolder {
				return p.cache.Snapshot().RootFoldersForCategory(categoryID)
			},
			Clock: clock.NewSystem(), Logger: p.logger,
		}},
		Searching: tv.Searching{Acquire: p.tvGrab(), Conns: p.tvIndexerConns, Clock: clock.NewSystem()},
		Following: tv.Following{
			People: library.NewPeople(p.library), Subscriptions: library.NewSubscriptions(p.library), Source: p.tmdbShared,
			Service: p.tvService(), DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
			Clock: clock.NewSystem(),
		},
		Profiling: tv.Profiling{People: library.NewPeople(p.library), Reader: library.NewSeriesReader(p.library), Source: p.tmdbShared},
	}
}

// musicHunt searches one album the hunt chose and sends the best release.
func (p *process) musicHunt(ctx context.Context, itemID int64) (plan.Outcome, error) {
	indexers, err := p.indexerConns(ctx)
	if err != nil || len(indexers) == 0 {
		return plan.OutcomeSearched, err
	}
	result, err := p.musicGrab().SearchAndSend(ctx, itemID, indexers, music.GrabAutomatic, clock.NewSystem().Now())
	switch {
	case errors.Is(err, music.ErrTrackSearchNotSupported):
		return plan.OutcomeNotWanted, nil
	case err != nil:
		return plan.OutcomeSearched, err
	case result.AlreadyInFlight:
		return plan.OutcomeInFlight, nil
	}
	if result.Sent != nil || result.Held {
		p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
	}
	if len(result.Refusals) > 0 {
		p.logger.Info("hunt: releases refused", slog.Int64("item_id", itemID), slog.Any("refused", result.Refusals))
	}
	return plan.OutcomeSearched, nil
}

// tvHunt searches one season the hunt chose and sends the best release.
func (p *process) tvHunt(ctx context.Context, itemID int64) (plan.Outcome, error) {
	conns, err := p.tvIndexerConns(ctx)
	if err != nil {
		return plan.OutcomeSearched, err
	}
	result, err := p.tvGrab().SearchAndSend(ctx, itemID, conns, dispatch.GrabAutomatic, clock.NewSystem().Now())
	switch {
	case errors.Is(err, tv.ErrNotSearchable):
		return plan.OutcomeNotWanted, nil
	case err != nil:
		return plan.OutcomeSearched, err
	case result.AlreadyInFlight:
		return plan.OutcomeInFlight, nil
	}
	if result.Sent != nil || result.Held {
		p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
	}
	return plan.OutcomeSearched, nil
}

// musicAdder is music's lookup and add: albums become wanted, artists followed.
func (p *process) musicAdder() adapters.Discover {
	return adapters.Discover{
		Provider: p.musicBrainzProvider(), Identity: library.NewIdentity(p.library),
		Desired: library.NewDesired(p.library), People: library.NewPeople(p.library),
		Categories:       adapters.Categories{Cache: p.cache},
		DefaultProfileID: p.defaultQualityProfileID, Clock: clock.NewSystem(),
		// A failed enqueue is logged by enrichmentPass; the hourly sweep still picks the album up.
		Enrich: func(ctx context.Context) { _, _ = p.enrichmentPass(ctx, false) },
		SearchAlbum: func(ctx context.Context, categoryID, itemID int64) {
			if _, err := music.EnqueueSearchItems(ctx, p.queue, categoryID, []int64{itemID}); err != nil {
				p.logger.Warn("search added album", slog.Any("error", err))
			}
		},
		ExpandArtist: func(ctx context.Context, subscriptionID int64, search bool) {
			if _, err := follow.Enqueue(ctx, p.queue, subscriptionID, search); err != nil {
				p.logger.Warn("add artist's albums", slog.Any("error", err))
			}
		},
	}
}

// musicFiles places a music file nothing identified as the album a person names.
func (p *process) musicFiles() adapters.MusicFiles {
	return adapters.MusicFiles{
		Unidentified: library.NewUnidentified(p.library), Identity: library.NewIdentity(p.library),
		Provider: p.musicBrainzProvider(), Clock: clock.NewSystem(),
	}
}

// enqueueSharedFiles submits the pass placing episodes two series share into both.
func (p *process) enqueueSharedFiles(ctx context.Context) error {
	return tv.EnqueueSharedFiles(ctx, p.queue)
}

// enqueueServerFilesFor submits the pass for one series, right after an import placed episodes of it.
func (p *process) enqueueServerFilesFor(ctx context.Context, categoryID, workID int64) {
	if _, err := tv.EnqueueServerFilesFor(ctx, p.queue, categoryID, workID); err != nil {
		p.logger.Warn("could not queue the media server files of an imported series", "work_id", workID, "error", err)
	}
}

// enqueueServerFiles submits the pass writing Jellyfin and Kodi files.
func (p *process) enqueueServerFiles(ctx context.Context) (jobs.EnqueueResult, error) {
	return tv.EnqueueServerFiles(ctx, p.queue)
}

// musicJobs are the job types the music module brings.
func (p *process) musicJobs() []media.JobRegistration {
	registrations := p.enrichJobs()
	return append(registrations, p.tagWritebackJob(), p.artworkJob(), p.discographyCoverageJob(), p.searchCategoryJob())
}

// tvJobs are the job types the TV module brings besides its own typed ones.
func (p *process) tvJobs() []media.JobRegistration {
	return []media.JobRegistration{p.tvRefreshJob(), p.bridgeJob(), p.tvArtworkJob()}
}
