package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/housekeeping"
	"github.com/makearr/Solarr/internal/media/knownfor"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/internal/service/follow"
)

// registerMedia builds the one list of media types in the binary, each wired to what it needs from the process.
// A new type is one more module here; nothing else in this package names a type's scan.
func (p *process) registerMedia() error {
	modules := []media.Module{p.musicModule(), p.tvModule(), p.movieModule()}
	for _, format := range book.Formats {
		modules = append(modules, p.bookModule(format))
	}
	registry, err := media.NewRegistry(append(modules, p.extraMedia...)...)
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
	rename := music.RenameConfig{Works: library.NewWorks(p.library)}
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
	return tv.Module{Registrations: p.tvJobs(), SourcesPaused: p.tvSourcesPaused, TMDBConfigured: func() bool { return p.cache.Snapshot().TMDBConfigured() },
		Sweep: scan.Sweep{
			Consumer: consumer, Include: tv.IsVideo,
			OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx) },
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: tv.Module{}.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
		},
		Hunt:     p.tvHunt,
		Rename:   &tv.RenameConfig{Series: library.NewSeriesReader(p.library), Clock: clock.NewSystem()},
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
			Libraries: p.libraryIDsOf(tv.MediaType),
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
		Franchise: tv.Franchising{
			Source: p.wikidataFetcher(), Franchises: library.NewFranchises(p.library), Series: p.tvService().Series, Service: p.tvService(),
			DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache}, Pictures: tv.NewMissingPictures(),
		},
		Crossovers: &tv.CrossoverJob{
			Crossovers: library.NewCrossovers(p.library), Logger: p.logger,
			Libraries: p.libraryIDsOf(tv.MediaType),
		},
		FranchisePass: &tv.FranchisePass{
			Source: p.wikidataFetcher(), Anime: p.aniListClient(), Franchises: library.NewFranchises(p.library), Crossovers: library.NewCrossovers(p.library),
			Series: p.tvService().Series, Logger: p.logger,
			Again: func(ctx context.Context) {
				_, _ = p.queue.Enqueue(ctx, jobs.Request{
					Type: tv.FranchisePassJobType, ActionKey: "tv.franchises.continue", Priority: jobs.PriorityLow, RunAfter: clock.NewSystem().Now().Add(5 * time.Minute),
				})
			},
		},
		Following: tv.Following{
			People: library.NewPeople(p.library), Subscriptions: library.NewSubscriptions(p.library), Source: p.tmdbShared,
			Service: p.tvService(), DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
			Clock: clock.NewSystem(),
		},
		Profiling: tv.Profiling{People: library.NewPeople(p.library), Reader: library.NewSeriesReader(p.library), Source: p.tmdbShared, Albums: p.knownAlbums()},
	}
}

// knownAlbums lists an actor's albums on their person page.
func (p *process) knownAlbums() music.KnownAlbums {
	return music.KnownAlbums{Wikidata: p.wikidataFetcher(), Provider: p.musicBrainzProvider(), Identity: library.NewIdentity(p.library)}
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

// enqueueMovieServerFilesFor submits the pass for one movie, right after an import placed a file of it.
func (p *process) enqueueMovieServerFilesFor(ctx context.Context, categoryID, workID int64) {
	if _, err := movie.EnqueueServerFilesFor(ctx, p.queue, categoryID, workID); err != nil {
		p.logger.Warn("could not queue the media server files of an imported movie", "work_id", workID, "error", err)
	}
}

// enqueueServerFiles submits the passes writing Jellyfin and Kodi files, one per media type that has them.
func (p *process) enqueueServerFiles(ctx context.Context) (jobs.EnqueueResult, error) {
	if _, err := movie.EnqueueServerFiles(ctx, p.queue); err != nil {
		return jobs.EnqueueResult{}, err
	}
	return tv.EnqueueServerFiles(ctx, p.queue)
}

// musicJobs are the job types the music module brings.
func (p *process) musicJobs() []media.JobRegistration {
	registrations := p.enrichJobs()
	labelLogos := media.JobRegistration{Definition: music.LabelLogosDefinition, Handler: music.LabelLogosJob{
		Organisations: library.NewOrganisations(p.library), Source: p.wikidataFetcher(), Clock: clock.NewSystem(), Logger: p.logger,
	}}
	return append(registrations, p.tagWritebackJob(), p.artworkJob(), p.discographyCoverageJob(), p.searchCategoryJob(), labelLogos)
}

// tvJobs are the job types the TV module brings besides its own typed ones.
func (p *process) tvJobs() []media.JobRegistration {
	return []media.JobRegistration{p.tvRefreshJob(), p.bridgeJob(), p.tvArtworkJob(), p.tvTitleRenameJob()}
}

// tvTitleRenameJob renames episode files named TBA once their title has arrived, through the one rename engine.
func (p *process) tvTitleRenameJob() media.JobRegistration {
	engine := p.renameEngine()
	return media.JobRegistration{Definition: tv.TitleRenameDefinition, Handler: tv.NewTitleRenameJob(tv.TitleRenameConfig{
		Files: library.NewPhysical(p.library),
		Namer: tv.RenameConfig{Series: library.NewSeriesReader(p.library), Clock: clock.NewSystem()},
		Rename: func(ctx context.Context, namer renamejob.Namer, category settings.Category, rootFolderID, workID int64) error {
			_, _, _, err := engine.Start(ctx, namer, category, rootFolderID, workID, false)
			return err
		},
		Libraries: func() []tv.TitleRenameLibrary {
			snapshot := p.cache.Snapshot()
			var libraries []tv.TitleRenameLibrary
			for _, category := range snapshot.Categories() {
				if category.MediaType == tv.MediaType {
					libraries = append(libraries, tv.TitleRenameLibrary{Category: category, Roots: snapshot.RootFoldersForCategory(category.ID)})
				}
			}
			return libraries
		},
		Logger: p.logger,
	})}
}

// tvTitleRenameTask is the registered "Rename episodes whose title arrived" task.
func (p *process) tvTitleRenameTask() jobs.Task {
	return jobs.Task{
		Key: tv.TitleRenameScheduleKey, Name: "Rename episodes whose title arrived", DefaultCadence: tvTitleRenameScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) {
			result, err := tv.EnqueueTitleRename(ctx, p.queue)
			if err != nil {
				p.logger.Warn("enqueue tv title rename", slog.Any("error", err))
			}
			return result, err
		},
	}
}

// movieModule wires movies: the sweep that finds movie files, the add and refresh over TMDB and Radarr's service, and the
// jobs that keep the metadata and pictures current.
func (p *process) movieModule() movie.Module {
	consumer := movie.NewConsumer(movie.ConsumerConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library),
		Service: p.movieService(), Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
	})
	catalog := p.movieCatalog()
	return movie.Module{
		Registrations:  p.movieJobs(),
		TMDBConfigured: func() bool { return p.cache.Snapshot().TMDBConfigured() },
		Sweep: scan.Sweep{
			Consumer: consumer, Include: movie.IsFeature,
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: movie.Module{}.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
			OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx) },
		},
		Adder: catalog, Franchise: catalog,
		Rename:    &movie.RenameConfig{Movies: library.NewSeriesReader(p.library)},
		Profiling: knownfor.Profiling{People: library.NewPeople(p.library), Reader: library.NewSeriesReader(p.library), Source: p.tmdbShared, Albums: p.knownAlbums()},
		Files: movie.Files{
			Resolver: movie.Resolver{
				Service: p.movieService(), Reader: library.NewSeriesReader(p.library), Logger: p.logger,
				DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
			},
			Series: library.NewSeries(p.library), Unidentified: library.NewUnidentified(p.library), Clock: clock.NewSystem(),
			AllowAgain: catalog.AllowAgain,
		},
		ServerFiles: &movie.ServerFilesJob{
			Writer: movie.ServerFiles{
				Sidecar: sidecar.Writer{FS: p.fsys, Picture: p.remoteImages().Original}, Reader: library.NewSeriesReader(p.library),
				Physical: library.NewPhysical(p.library), Identity: library.NewIdentity(p.library),
				Roots: adapters.Roots{Cache: p.cache}, Clock: clock.NewSystem(),
			},
			Libraries: func() []settings.Category { return p.cache.Snapshot().Categories() },
			RootsOf: func(categoryID int64) []settings.RootFolder {
				return p.cache.Snapshot().RootFoldersForCategory(categoryID)
			},
			Logger: p.logger,
		},
		Hunt: p.movieHunt,
		Feed: movie.Feed{
			Grab: p.movieGrab(), Reader: library.NewSeriesReader(p.library), Indexers: p.movieIndexerConns, Logger: p.logger,
			Libraries: p.libraryIDsOf(movie.MediaType),
		},
		CutoffJob: movie.CutoffJob{
			Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library), Settings: p.cache,
			Recycle: p.recycleSuperseded, Logger: p.logger,
		},
		Searching: movie.NewSearching(p.movieGrab(), p.movieIndexerConns, clock.NewSystem()),
		Pictures:  adapters.ProviderPictures{Reader: library.NewSeriesReader(p.library), Series: library.NewSeries(p.library), Clock: clock.NewSystem()},
		Service:   p.movieService(),
		// A movie is read again at once, as Radarr's Refresh: it is one work and a couple of requests.
		Refresh: func(ctx context.Context, workID int64) (bool, error) {
			_, libraries, err := library.NewWorks(p.library).Places(ctx, workID)
			if err != nil {
				return false, err
			}
			if len(libraries) == 0 {
				return false, fmt.Errorf("movie %d is in no library", workID)
			}
			return false, catalog.Refresh(ctx, libraries[0], workID)
		},
		Passes: media.Passes{
			WorksPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := movie.EnqueueRefresh(ctx, p.queue)
				return result.Duplicate, err
			},
			PicturesPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := movie.EnqueueArtwork(ctx, p.queue)
				return result.Duplicate, err
			},
		},
	}
}

// movieJobs are the job types the movie module brings.
func (p *process) movieJobs() []media.JobRegistration {
	return []media.JobRegistration{
		{Definition: movie.RefreshDefinition, Handler: movie.NewRefreshJob(movie.RefreshConfig{
			Reader: library.NewSeriesReader(p.library), Service: p.movieService(), Clock: clock.NewSystem(), Logger: p.logger,
		})},
		{Definition: movie.UniversePassDefinition, Handler: &movie.UniversePass{
			Source: p.wikidataFetcher(), Franchises: library.NewFranchises(p.library), Logger: p.logger,
		}},
		{Definition: movie.ArtworkDefinition, Handler: movie.NewArtworkJob(library.NewSeriesReader(p.library), p.remoteImages(), p.logger)},
	}
}

// movieGrab builds the movie module's automatic search over the real stores.
func (p *process) movieGrab() movie.Grab {
	blocklist, sightings, filters := library.NewBlocklistStore(p.library), library.NewSightings(p.library), library.NewReleaseFilters(p.library)
	return movie.Grab{
		Search: movie.Search{
			Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library), Candidates: statistics.NewCandidates(p.statistics),
			Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist, Sightings: &sightings, ReleaseFilters: &filters,
		},
		Automatic: dispatch.Automatic{
			Dispatch: p.dispatchFor(movie.MediaType), Name: "movie",
			FreeSpace: func(categoryID int64) (int64, bool) { return p.cache.Snapshot().AcquisitionHeadroom(categoryID) },
		},
	}
}

// movieHunt searches one movie the hunt chose and sends the best release.
func (p *process) movieHunt(ctx context.Context, itemID int64) (plan.Outcome, error) {
	conns, err := p.movieIndexerConns(ctx)
	if err != nil {
		return plan.OutcomeSearched, err
	}
	result, err := p.movieGrab().SearchAndSend(ctx, itemID, conns, dispatch.GrabAutomatic, clock.NewSystem().Now())
	switch {
	case errors.Is(err, movie.ErrNotSearchable):
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

// enqueueUniversePass submits the pass that groups films into Wikidata's franchises and universes.
func (p *process) enqueueUniversePass(ctx context.Context) (jobs.EnqueueResult, error) {
	return movie.EnqueueUniversePass(ctx, p.queue)
}

// enqueueFranchisePass submits the pass that finds the franchises series belong to.
func (p *process) enqueueFranchisePass(ctx context.Context) (jobs.EnqueueResult, error) {
	return tv.EnqueueFranchisePass(ctx, p.queue)
}

// enqueueCrossoverPass submits the pass that finds crossover episodes.
func (p *process) enqueueCrossoverPass(ctx context.Context) (jobs.EnqueueResult, error) {
	return tv.EnqueueCrossoverPass(ctx, p.queue)
}

// bookModule wires one of the two book types: the sweep that finds its files, the add and refresh over Open Library,
// Google Books and Audible, the search and the jobs that keep its metadata and covers current.
func (p *process) bookModule(format book.Format) book.Module {
	service := p.bookService(format)
	catalog := p.bookCatalog(format)
	authors := book.AuthorWorks{Format: format, People: library.NewPeople(p.library), Reader: library.NewSeriesReader(p.library), Source: service.OpenLibrary}
	resolver := book.Resolver{
		Service: service, Logger: p.logger, DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
		Locks: &keyedlock.Locks{},
	}
	grab := p.bookGrab(format)
	conns := p.bookIndexerConns(format)
	module := book.Module{Format: format}
	return book.Module{
		Format: format,
		Registrations: []media.JobRegistration{
			{Definition: format.RefreshDefinition(), Handler: format.NewRefreshJob(library.NewSeriesReader(p.library), service, clock.NewSystem(), p.logger)},
			{Definition: format.ArtworkDefinition(), Handler: format.NewArtworkJob(library.NewSeriesReader(p.library), p.remoteImages(), p.logger)},
		},
		Sweep: scan.Sweep{
			Consumer: book.NewConsumer(book.ConsumerConfig{
				FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library), Service: service,
				Clock: clock.NewSystem(), Logger: p.logger, DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache},
			}),
			Include: format.Includes, Probe: format.Probes,
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: module.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
			OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx) },
		},
		Adder:    catalog,
		Pictures: adapters.ProviderPictures{Reader: library.NewSeriesReader(p.library), Series: library.NewSeries(p.library), Clock: clock.NewSystem()},
		Files:    book.NewFiles(resolver, library.NewUnidentified(p.library), clock.NewSystem(), catalog.AllowAgain),
		Rename:   &book.RenameConfig{Format: format, Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem()},
		Authors:  &authors,
		Follow:   &book.Following{Authors: authors, Catalog: catalog},
		Series: &book.SeriesBooks{
			Franchises: library.NewFranchises(p.library), Reader: library.NewSeriesReader(p.library), Source: p.wikidataFetcher(), Catalog: catalog,
		},
		Hunt: func(ctx context.Context, itemID int64) (plan.Outcome, error) {
			found, err := conns(ctx)
			if err != nil {
				return plan.OutcomeSearched, err
			}
			result, err := grab.SearchAndSend(ctx, itemID, found, dispatch.GrabAutomatic, clock.NewSystem().Now())
			switch {
			case errors.Is(err, book.ErrNotSearchable):
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
		},
		Feed: book.Feed{
			Format: format, Grab: grab, Reader: library.NewSeriesReader(p.library), Indexers: conns, Logger: p.logger, Libraries: p.libraryIDsOf(format.MediaType),
		},
		CutoffJob: book.CutoffJob{
			Format: format, Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library), Physical: library.NewPhysical(p.library),
			Settings: p.cache, Recycle: p.recycleSuperseded, Logger: p.logger,
		},
		Searching: format.NewSearching(grab, conns, clock.NewSystem()),
		Service:   service,
		// A book is read again at once, as Readarr's Refresh: it is one work and a few requests.
		Refresh: func(ctx context.Context, workID int64) (bool, error) {
			_, libraries, err := library.NewWorks(p.library).Places(ctx, workID)
			if err != nil {
				return false, err
			}
			if len(libraries) == 0 {
				return false, fmt.Errorf("%s %d is in no library", format.MediaType, workID)
			}
			return false, catalog.Refresh(ctx, libraries[0], workID)
		},
		Passes: media.Passes{
			WorksPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := format.EnqueueRefresh(ctx, p.queue)
				return result.Duplicate, err
			},
			PicturesPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := format.EnqueueArtwork(ctx, p.queue)
				return result.Duplicate, err
			},
		},
	}
}

// bookGrab builds a book type's automatic search over the real stores.
func (p *process) bookGrab(format book.Format) book.Grab {
	blocklist, sightings, filters := library.NewBlocklistStore(p.library), library.NewSightings(p.library), library.NewReleaseFilters(p.library)
	return book.Grab{
		Search: book.Search{
			Format: format, Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library), Physical: library.NewPhysical(p.library),
			Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist,
			Sightings: &sightings, ReleaseFilters: &filters,
		},
		Automatic: dispatch.Automatic{
			Dispatch: p.dispatchFor(format.MediaType), Name: format.MediaType,
			FreeSpace: func(categoryID int64) (int64, bool) { return p.cache.Snapshot().AcquisitionHeadroom(categoryID) },
		},
	}
}
