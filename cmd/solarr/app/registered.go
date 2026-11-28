package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/adult"
	"github.com/makearr/Solarr/internal/media/adultfeeds"
	"github.com/makearr/Solarr/internal/media/anifeeds"
	"github.com/makearr/Solarr/internal/media/audiblefeeds"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/comic"
	"github.com/makearr/Solarr/internal/media/deezerfeeds"
	"github.com/makearr/Solarr/internal/media/hcfeeds"
	"github.com/makearr/Solarr/internal/media/housekeeping"
	"github.com/makearr/Solarr/internal/media/knownfor"
	"github.com/makearr/Solarr/internal/media/lastfmfeeds"
	"github.com/makearr/Solarr/internal/media/lbfeeds"
	"github.com/makearr/Solarr/internal/media/mangafeeds"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/olfeeds"
	"github.com/makearr/Solarr/internal/media/tmdbfeeds"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/metadata/comicvine"
	"github.com/makearr/Solarr/internal/metadata/deezer"
	"github.com/makearr/Solarr/internal/metadata/gcd"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/metadata/lastfm"
	"github.com/makearr/Solarr/internal/metadata/listenbrainz"
	"github.com/makearr/Solarr/internal/metadata/mangadex"
	"github.com/makearr/Solarr/internal/metadata/mangaupdates"
	"github.com/makearr/Solarr/internal/metadata/metron"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/internal/service/follow"
)

// registerMedia builds the one list of media types in the binary, each wired to what it needs from the process.
// A new type is one more module here; nothing else in this package names a type's scan.
func (p *process) registerMedia() error {
	modules := []media.Module{p.musicModule(), p.tvModule(), p.movieModule(), p.adultModule(), p.comicModule()}
	for _, format := range book.Formats {
		modules = append(modules, p.bookModule(format))
	}
	registry, err := media.NewRegistry(append(modules, p.extraMedia...)...)
	if err != nil {
		return err
	}
	p.media = registry
	p.cache.EnableNamingValidation(adapters.NamingValidator(registry))
	p.cache.EnableDownloadUsage(library.NewAcquisitions(p.library))
	p.cache.EnableLibraryUsage(library.NewUsage(p.library))
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
		Servers:      musicServerAsker{asker: p.localMetadata()},
		Albums:       provider,
		Fingerprints: p.tool,
		AcoustID:     p.acoustIDLookup(),
		Tracks:       provider,
		Clock:        clock.NewSystem(),
		Logger:       p.logger,
		// One identification threshold for scan and import (2026-09-24).
		Threshold:  p.acceptThreshold,
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
			_, err := music.EnqueueSearchArtist(ctx, p.queue, jobs.PriorityLow, categoryID, personID)
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
	return music.Module{Feeds: p.musicFeeds(), AcoustIDConfigured: func() bool { return p.cache.Snapshot().AcoustIDConfigured() }, Registrations: p.musicJobs(), Realign: realign, Files: p.musicFiles(), Passes: passes, Feed: feed, CutoffJob: check, Following: following, Searching: searching, Rename: &rename, Sweep: scan.Sweep{
		Consumer: consumer,
		Include:  music.IsMusicFile,
		Housekeeping: music.NewHousekeeping(music.HousekeepingConfig{
			FS: p.fsys, Categories: adapters.Categories{Cache: p.cache}, Roots: adapters.DrainRoots{Cache: p.cache},
			Affinity: p.cache, Desired: &desired, Subscriptions: library.NewSubscriptions(p.library),
			People: library.NewPeople(p.library), GroupID: lookupGroupID,
		}),
		// Enrichment follows at once, not at the next hourly run: a new library had no tracklists,
		// photos or Wanted for up to an hour (found 2026-09-28).
		OnSwept: func(ctx context.Context) { p.afterSweep(ctx); _, _ = p.enrichmentPass(ctx, false) },
	},
		Wiring: media.Wiring{Hunt: p.musicHunt, Refresh: refresh, Adder: p.musicAdder(), Pictures: p.musicPictures()},
	}
}

// tvModule wires TV's sweep; its waiting files are identified again by the shared identify-retry task.
func (p *process) tvModule() tv.Module {
	consumer := tv.NewConsumer(tv.ConsumerConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library),
		Service: p.tvService(), Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		Categories: adapters.Categories{Cache: p.cache}, Resolver: p.tvResolver(), SharedFiles: p.enqueueSharedFiles,
	})
	return tv.Module{Feeds: p.tmdbFeeds(tmdb.MediaTV), Registrations: p.tvJobs(), SourcesPaused: p.tvSourcesPaused, SceneNumberingDown: p.sceneNumberingDown, TMDBConfigured: func() bool { return p.cache.Snapshot().TMDBConfigured() },
		Sweep: scan.Sweep{
			Consumer: consumer, Include: tv.IsVideo,
			OnSwept: p.afterSweep,
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: tv.Module{}.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
		},
		Rename:   &tv.RenameConfig{Series: library.NewSeriesReader(p.library), Clock: clock.NewSystem()},
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
		Feed: tv.Feed{
			Grab: p.tvGrab(), Reader: library.NewSeriesReader(p.library), Indexers: p.tvIndexerConns, Logger: p.logger,
			Libraries: p.libraryIDsOf(tv.MediaType),
		},
		CutoffJob: tv.CutoffJob{
			Desired: library.NewDesired(p.library), Series: library.NewSeriesReader(p.library), Settings: p.cache,
			Recycle: p.recycleSuperseded, HandOver: p.handOverSidecars, Logger: p.logger,
		},
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
			Series: p.tvService().Series, Logger: p.logger, Episodes: p.tvService(), Lists: p.tvdbClient(), Clock: clock.NewSystem(),
			Films: p.tvdbClient(), FilmType: movie.MediaType,
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
		Lists:     tv.Lists{Service: p.tvService()},
		Wiring: media.Wiring{Hunt: p.tvHunt, Adder: p.seriesCatalog(), Pictures: p.seriesCatalog(), // A series is read again at once, as Sonarr's Refresh: it is one work and a handful of requests.
			Refresh: func(ctx context.Context, workID int64) (bool, error) {
				libraries, err := p.workLibraries(ctx, workID)
				if err != nil {
					return false, err
				}
				if len(libraries) == 0 {
					return false, fmt.Errorf("series %d is in no library", workID)
				}
				return false, p.seriesCatalog().Refresh(ctx, libraries[0], workID)
			},
		},
	}
}

// knownAlbums lists an actor's albums on their person page.
func (p *process) knownAlbums() music.KnownAlbums {
	return music.KnownAlbums{Wikidata: p.wikidataFetcher(), Provider: p.musicBrainzProvider(), Identity: library.NewIdentity(p.library)}
}

// musicHunt searches one album the hunt chose and sends the best release.
func (p *process) musicHunt(ctx context.Context, itemID int64) (plan.Outcome, error) {
	indexers, err := p.indexerConns(ctx)
	if err != nil {
		return plan.OutcomeSearched, err
	}
	if len(indexers) == 0 {
		return plan.OutcomeNoAnswer, nil
	}
	result, err := p.musicGrab().SearchAndSend(ctx, itemID, indexers, music.GrabAutomatic, clock.NewSystem().Now())
	switch {
	case errors.Is(err, music.ErrTrackSearchNotSupported):
		return plan.OutcomeNotWanted, nil
	case err != nil:
		return plan.OutcomeSearched, err
	case result.AlreadyInFlight:
		return plan.OutcomeInFlight, nil
	case result.Search.Answered == 0:
		return plan.OutcomeNoAnswer, nil
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
	case result.Search.Answered == 0:
		return plan.OutcomeNoAnswer, nil
	}
	if result.Sent != nil || result.Held {
		p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
	}
	return plan.OutcomeSearched, nil
}

// musicAdder is music's lookup and add: albums become wanted, artists followed.
func (p *process) musicAdder() music.Adder {
	return music.Adder{
		Provider: p.musicBrainzProvider(), Identity: library.NewIdentity(p.library),
		Desired: library.NewDesired(p.library), People: library.NewPeople(p.library),
		Categories:       adapters.Categories{Cache: p.cache},
		DefaultProfileID: p.defaultQualityProfileID, Clock: clock.NewSystem(), Logger: p.logger,
		// A failed enqueue is logged by enrichmentPass; the hourly sweep still picks the album up.
		Enrich: func(ctx context.Context) { _, _ = p.enrichmentPass(ctx, false) },
		SearchAlbum: func(ctx context.Context, categoryID, itemID int64) {
			if _, err := music.EnqueueSearchItems(ctx, p.queue, jobs.PriorityInteractive, categoryID, []int64{itemID}); err != nil {
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
	p.recordServerPush(ctx, categoryID, workID, library.PendingCreated)
}

// enqueueReaderFilesFor submits the reader files pass for one comic series, right after an import placed files of it.
func (p *process) enqueueReaderFilesFor(ctx context.Context, categoryID, workID int64) {
	if _, err := comic.EnqueueReaderFilesFor(ctx, p.queue, categoryID, workID); err != nil {
		p.logger.Warn("could not queue the reader files of an imported comic series", "work_id", workID, "error", err)
	}
	p.recordServerPush(ctx, categoryID, workID, library.PendingCreated)
}

// enqueueMovieServerFilesFor submits the pass for one movie, right after an import placed a file of it.
func (p *process) enqueueMovieServerFilesFor(ctx context.Context, categoryID, workID int64) {
	if _, err := movie.EnqueueServerFilesFor(ctx, p.queue, categoryID, workID); err != nil {
		p.logger.Warn("could not queue the media server files of an imported movie", "work_id", workID, "error", err)
	}
	p.recordServerPush(ctx, categoryID, workID, library.PendingCreated)
}

// enqueueServerFiles submits the passes writing Jellyfin and Kodi files, one per media type that has them.
func (p *process) enqueueServerFiles(ctx context.Context) (jobs.EnqueueResult, error) {
	if _, err := movie.EnqueueServerFiles(ctx, p.queue); err != nil {
		return jobs.EnqueueResult{}, err
	}
	if _, err := comic.EnqueueReaderFiles(ctx, p.queue); err != nil {
		return jobs.EnqueueResult{}, err
	}
	return tv.EnqueueServerFiles(ctx, p.queue)
}

// rehashFile records the size and quick hash a file rewritten in place has now.
func (p *process) rehashFile(ctx context.Context, mediaObjectID int64, filePath string) error {
	info, err := p.fsys.Stat(filePath)
	if err != nil {
		return err
	}
	quick, err := library.QuickHash(p.fsys, filePath, info.Size)
	if err != nil {
		return err
	}
	return library.NewTagWriteback(p.library).Rehash(ctx, mediaObjectID, info.Size, quick)
}

// musicJobs are the job types the music module brings.
func (p *process) musicJobs() []media.JobRegistration {
	registrations := p.enrichJobs()
	labelLogos := media.JobRegistration{Definition: music.LabelLogosDefinition, Handler: music.LabelLogosJob{
		Organisations: library.NewOrganisations(p.library), Source: p.wikidataFetcher(), Clock: clock.NewSystem(), Logger: p.logger,
	}}
	collections := media.JobRegistration{Definition: music.CollectionPassDefinition, Handler: music.CollectionPass{
		Wikidata: p.wikidataFetcher(), MusicBrainz: p.musicBrainzProvider(), Franchises: library.NewFranchises(p.library), Logger: p.logger,
		Libraries: adapters.EntityLibraries{
			MediaType: string(music.MediaType), Works: library.NewWorks(p.library), People: library.NewPeople(p.library), Cache: p.cache,
		},
	}}
	return append(registrations, p.lyricsJob(), p.tagWritebackJob(), p.artworkJob(), p.discographyCoverageJob(), p.searchCategoryJob(), labelLogos, collections)
}

// enqueueMusicCollections submits the pass that puts albums in the series and franchises they belong to.
func (p *process) enqueueMusicCollections(ctx context.Context) (jobs.EnqueueResult, error) {
	return music.EnqueueCollectionPass(ctx, p.queue)
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
		Service: p.movieService(), Clock: clock.NewSystem(), Logger: p.logger, Resolver: p.movieResolver(),
	})
	catalog := p.movieCatalog()
	return movie.Module{
		Feeds:          p.tmdbFeeds(tmdb.MediaMovie),
		Registrations:  p.movieJobs(),
		TMDBConfigured: func() bool { return p.cache.Snapshot().TMDBConfigured() },
		Sweep: scan.Sweep{
			Consumer: consumer, Include: movie.IsFeature,
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: movie.Module{}.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
			OnSwept: p.afterSweep,
		}, Franchise: catalog,
		Rename:    &movie.RenameConfig{Movies: library.NewSeriesReader(p.library)},
		Profiling: knownfor.Profiling{People: library.NewPeople(p.library), Reader: library.NewSeriesReader(p.library), Source: p.tmdbShared, Albums: p.knownAlbums()},
		Files: movie.Files{
			Resolver: p.movieResolver(),
			Series:   library.NewSeries(p.library), Unidentified: library.NewUnidentified(p.library), Clock: clock.NewSystem(),
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
		Feed: movie.Feed{
			Grab: p.movieGrab(), Reader: library.NewSeriesReader(p.library), Indexers: p.movieIndexerConns, Logger: p.logger,
			Libraries: p.libraryIDsOf(movie.MediaType),
		},
		CutoffJob: movie.CutoffJob{
			Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library), Settings: p.cache,
			Recycle: p.recycleSuperseded, Logger: p.logger,
		},
		Searching: movie.NewSearching(p.movieGrab(), p.movieIndexerConns, clock.NewSystem()),
		Service:   p.movieService(),
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
		Wiring: media.Wiring{Adder: catalog, Hunt: p.movieHunt, Pictures: adapters.ProviderPictures{Reader: library.NewSeriesReader(p.library), Series: library.NewSeries(p.library), Clock: clock.NewSystem()}, // A movie is read again at once, as Radarr's Refresh: it is one work and a couple of requests.
			Refresh: func(ctx context.Context, workID int64) (bool, error) {
				libraries, err := p.workLibraries(ctx, workID)
				if err != nil {
					return false, err
				}
				if len(libraries) == 0 {
					return false, fmt.Errorf("movie %d is in no library", workID)
				}
				return false, catalog.Refresh(ctx, libraries[0], workID)
			},
		},
	}
}

// movieJobs are the job types the movie module brings.
func (p *process) movieJobs() []media.JobRegistration {
	return []media.JobRegistration{
		p.imdbJob(),
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
			Reader: library.NewSeriesReader(p.library),
			Deps:   works.Deps{Desired: library.NewDesired(p.library), Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist, Sightings: &sightings, ReleaseFilters: &filters},
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
	case result.Search.Answered == 0:
		return plan.OutcomeNoAnswer, nil
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

// bookSeriesKeysJob is the one pass that gives the series of both book types their Wikidata keys, brought by the first.
func (p *process) bookSeriesKeysJob(format book.Format) []media.JobRegistration {
	if format.MediaType != book.Formats[0].MediaType {
		return nil
	}
	return []media.JobRegistration{{Definition: book.SeriesKeysDefinition, Handler: book.SeriesKeysPass{
		Formats: book.Formats, Source: p.wikidataFetcher(), Franchises: library.NewFranchises(p.library), Logger: p.logger,
	}}}
}

// enqueueBookSeriesKeys submits the pass that tells each book series its Wikidata item.
func (p *process) enqueueBookSeriesKeys(ctx context.Context) (jobs.EnqueueResult, error) {
	return book.EnqueueSeriesKeys(ctx, p.queue)
}

// bookModule wires one of the two book types: the sweep that finds its files, the add and refresh over Open Library,
// Google Books and Audible, the search and the jobs that keep its metadata and covers current.
func (p *process) bookModule(format book.Format) book.Module {
	service := p.bookService(format)
	catalog := p.bookCatalog(format)
	authors := book.AuthorWorks{Format: format, People: library.NewPeople(p.library), Reader: library.NewSeriesReader(p.library), Source: service.OpenLibrary}
	resolver := p.bookResolver(format)
	grab := p.bookGrab(format)
	conns := p.bookIndexerConns(format)
	module := book.Module{Format: format}
	return book.Module{
		Format: format, Feeds: p.bookFeeds(format),
		Registrations: append([]media.JobRegistration{
			{Definition: format.RefreshDefinition(), Handler: format.NewRefreshJob(library.NewSeriesReader(p.library), service, clock.NewSystem(), p.logger)},
			{Definition: format.ArtworkDefinition(), Handler: format.NewArtworkJob(library.NewSeriesReader(p.library), p.remoteImages(), p.logger)},
		}, p.bookSeriesKeysJob(format)...),
		Sweep: scan.Sweep{
			Consumer: book.NewConsumer(book.ConsumerConfig{
				FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library), Service: service,
				Clock: clock.NewSystem(), Logger: p.logger, Resolver: resolver,
			}),
			Include: format.Includes, Probe: format.Probes,
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: module.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
			OnSwept: p.afterSweep,
		},
		Files:   book.NewFiles(resolver, library.NewUnidentified(p.library), clock.NewSystem(), catalog.AllowAgain),
		Rename:  &book.RenameConfig{Format: format, Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem()},
		Authors: &authors,
		Follow:  &book.Following{Authors: authors, Catalog: catalog},
		Series: &book.SeriesBooks{
			Franchises: library.NewFranchises(p.library), Reader: library.NewSeriesReader(p.library), Source: p.wikidataFetcher(), Catalog: catalog,
			Lister: hardcoverSeries{client: hardcover.New(p.providerClient(providerHardcover, nil, httpx.ClientConfig{}), p.providerKey(settings.BookSourceHardcover))},
			Titles: library.NewWorks(p.library), Languages: p.bookLanguages,
		},
		Feed: book.Feed{
			Format: format, Grab: grab, Reader: library.NewSeriesReader(p.library), Indexers: conns, Logger: p.logger, Libraries: p.libraryIDsOf(format.MediaType),
		},
		CutoffJob: book.CutoffJob{
			Format: format, Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library),
			Settings: p.cache, Recycle: p.recycleSuperseded, Logger: p.logger,
		},
		Searching: format.NewSearching(grab, conns, clock.NewSystem()),
		Service:   service,
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
		Wiring: media.Wiring{Adder: catalog, Pictures: adapters.ProviderPictures{Reader: library.NewSeriesReader(p.library), Series: library.NewSeries(p.library), Clock: clock.NewSystem()}, Hunt: func(ctx context.Context, itemID int64) (plan.Outcome, error) {
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
			case result.Search.Answered == 0:
				return plan.OutcomeNoAnswer, nil
			}
			if result.Sent != nil || result.Held {
				p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
			}
			return plan.OutcomeSearched, nil
		}, // A book is read again at once, as Readarr's Refresh: it is one work and a few requests.
			Refresh: func(ctx context.Context, workID int64) (bool, error) {
				libraries, err := p.workLibraries(ctx, workID)
				if err != nil {
					return false, err
				}
				if len(libraries) == 0 {
					return false, fmt.Errorf("%s %d is in no library", format.MediaType, workID)
				}
				return false, catalog.Refresh(ctx, libraries[0], workID)
			},
		},
	}
}

// bookGrab builds a book type's automatic search over the real stores.
func (p *process) bookGrab(format book.Format) book.Grab {
	blocklist, sightings, filters := library.NewBlocklistStore(p.library), library.NewSightings(p.library), library.NewReleaseFilters(p.library)
	return book.Grab{
		Search: book.Search{
			Format: format,
			Reader: library.NewSeriesReader(p.library),
			Deps:   works.Deps{Desired: library.NewDesired(p.library), Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist, Sightings: &sightings, ReleaseFilters: &filters},
		},
		Automatic: dispatch.Automatic{
			Dispatch: p.dispatchFor(format.MediaType), Name: format.MediaType,
			FreeSpace: func(categoryID int64) (int64, bool) { return p.cache.Snapshot().AcquisitionHeadroom(categoryID) },
		},
	}
}

// adultModule wires the adult type's sweep: its consumer, the video files it reads, the add and refresh over the
// providers and the jobs that keep its metadata and pictures current.
func (p *process) adultModule() adult.Module {
	service := p.adultService()
	catalog := p.adultCatalog()
	grab := p.adultGrab()
	conns := p.adultIndexerConns
	resolver := p.adultResolver()
	return adult.Module{
		Registrations: []media.JobRegistration{
			{Definition: adult.RefreshDefinition(), Handler: adult.NewRefreshJob(library.NewSeriesReader(p.library), service, clock.NewSystem(), p.logger)},
			{Definition: adult.ArtworkDefinition(), Handler: adult.NewArtworkJob(library.NewSeriesReader(p.library), p.remoteImages(), p.logger)},
			{Definition: adult.PerformersDefinition(), Handler: adult.Performers{Service: service, People: library.NewPeople(p.library), Wikidata: p.wikidataFetcher(), Clock: clock.NewSystem(), Logger: p.logger}},
		},
		Sweep: scan.Sweep{
			Consumer: adult.NewConsumer(adult.ConsumerConfig{
				FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library), Service: service,
				Clock: clock.NewSystem(), Logger: p.logger, Resolver: resolver,
			}),
			Include:     adult.IsScene,
			GatherFiles: adult.ScanGather,
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: adult.Module{}.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
			OnSwept: p.afterSweep,
		},
		Files:  adult.NewFiles(resolver, library.NewUnidentified(p.library), clock.NewSystem(), catalog.AllowAgain),
		Rename: &adult.RenameConfig{Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem()},
		Follow: &adult.Following{
			Service: service, Catalog: catalog, People: library.NewPeople(p.library), Studios: library.NewSubscriptions(p.library),
			Now: func() time.Time { return clock.NewSystem().Now() },
		},
		Scenes:  &adult.PerformerScenes{Service: service, People: library.NewPeople(p.library)},
		Service: service,
		Feeds:   p.adultFeeds(),
		Feed: adult.Feed{
			Grab: grab, Reader: library.NewSeriesReader(p.library), Indexers: conns, Logger: p.logger, Libraries: p.libraryIDsOf(adult.MediaType),
		},
		CutoffJob: adult.CutoffJob{
			Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library), Settings: p.cache, Recycle: p.recycleSuperseded, Logger: p.logger,
		},
		Searching: adult.NewSearching(grab, conns, clock.NewSystem()),
		Passes: media.Passes{
			WorksPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := adult.EnqueueRefresh(ctx, p.queue)
				return result.Duplicate, err
			},
			PeoplePass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := adult.EnqueuePerformers(ctx, p.queue)
				return result.Duplicate, err
			},
			PicturesPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := adult.EnqueueArtwork(ctx, p.queue)
				return result.Duplicate, err
			},
		},
		Wiring: media.Wiring{Adder: catalog, Pictures: adapters.ProviderPictures{Reader: library.NewSeriesReader(p.library), Series: library.NewSeries(p.library), Clock: clock.NewSystem()}, Hunt: func(ctx context.Context, itemID int64) (plan.Outcome, error) {
			found, err := conns(ctx)
			if err != nil {
				return plan.OutcomeSearched, err
			}
			result, err := grab.SearchAndSend(ctx, itemID, found, dispatch.GrabAutomatic, clock.NewSystem().Now())
			switch {
			case errors.Is(err, adult.ErrNotSearchable):
				return plan.OutcomeNotWanted, nil
			case err != nil:
				return plan.OutcomeSearched, err
			case result.AlreadyInFlight:
				return plan.OutcomeInFlight, nil
			case result.Search.Answered == 0:
				return plan.OutcomeNoAnswer, nil
			}
			if result.Sent != nil || result.Held {
				p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
			}
			return plan.OutcomeSearched, nil
		}, // A scene is read again at once, as Whisparr's Refresh: it is one work and a few requests.
			Refresh: func(ctx context.Context, workID int64) (bool, error) {
				libraries, err := p.workLibraries(ctx, workID)
				if err != nil {
					return false, err
				}
				if len(libraries) == 0 {
					return false, fmt.Errorf("%s %d is in no library", adult.MediaType, workID)
				}
				return false, catalog.Refresh(ctx, libraries[0], workID)
			},
		},
	}
}

// adultGrab builds the adult type's automatic search over the real stores.
func (p *process) adultGrab() adult.Grab {
	blocklist, sightings, filters := library.NewBlocklistStore(p.library), library.NewSightings(p.library), library.NewReleaseFilters(p.library)
	return adult.Grab{
		Search: adult.Search{
			Reader:   library.NewSeriesReader(p.library),
			Physical: library.NewPhysical(p.library),
			Deps:     works.Deps{Desired: library.NewDesired(p.library), Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist, Sightings: &sightings, ReleaseFilters: &filters},
		},
		Automatic: dispatch.Automatic{
			Dispatch: p.dispatchFor(adult.MediaType), Name: adult.MediaType,
			FreeSpace: func(categoryID int64) (int64, bool) { return p.cache.Snapshot().AcquisitionHeadroom(categoryID) },
		},
	}
}

// acceptThreshold is import.auto_commit_confidence as the settings hold it now, for every media type's accept rule.
func (p *process) acceptThreshold() float64 { return p.cache.Snapshot().ImportAutoCommitConfidence() }

// personReaders are the sources that fill a person's page when it is opened: the performer servers and TMDB.
func (p *process) personReaders(people library.People) []namedPersonReader {
	performers := adult.Performers{Service: p.adultService(), People: people, Wikidata: p.wikidataFetcher(), Clock: clock.NewSystem(), Logger: p.logger}
	tmdbPeople := knownfor.Profiling{People: people, Reader: library.NewSeriesReader(p.library), Source: p.tmdbShared}
	return []namedPersonReader{
		{name: "performer servers", read: func(ctx context.Context, personID int64) error {
			if err := performers.Read(ctx, personID); err != nil && !errors.Is(err, adult.ErrNoServerKnows) {
				return err
			}
			return nil
		}},
		{name: "tmdb", read: tmdbPeople.Details},
	}
}

// refreshArtistNow refreshes a music artist whose page is open ahead of the background sweeps.
func (p *process) refreshArtistNow(ctx context.Context, personID int64) error {
	_, err := music.EnqueueRefreshArtistNow(ctx, p.queue, personID)
	return err
}

// The resolvers below are the only place a media type's resolver is built: scan, import and watch take the same
// value, so they share one lock and one threshold.

func (p *process) tvResolver() tv.Resolver {
	return tv.Resolver{
		Service: p.tvService(), Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache}, Threshold: p.acceptThreshold,
		Locks: p.locksFor("tv"),
	}
}

func (p *process) movieResolver() movie.Resolver {
	return movie.Resolver{
		Service: p.movieService(), Reader: library.NewSeriesReader(p.library), Logger: p.logger,
		DefaultProfileID: p.defaultQualityProfileID, Categories: adapters.Categories{Cache: p.cache}, Threshold: p.acceptThreshold,
		Locks: p.locksFor("movie"),
	}
}

func (p *process) bookResolver(format book.Format) book.Resolver {
	return book.Resolver{
		Service: p.bookService(format), Logger: p.logger, DefaultProfileID: p.defaultQualityProfileID,
		Categories: adapters.Categories{Cache: p.cache}, Threshold: p.acceptThreshold, Locks: p.locksFor("book"),
		Languages: p.bookLanguages,
	}
}

// bookLanguages are the languages a book library prefers: its editions, and the books a collection lists.
func (p *process) bookLanguages(int64) []string {
	return settings.LanguageProfileFor(p.cache.Snapshot().DefaultLanguages(), false).AudioPreferred
}

func (p *process) adultResolver() adult.Resolver {
	return adult.Resolver{
		Service: p.adultService(), Logger: p.logger, DefaultProfileID: p.defaultQualityProfileID,
		Categories: adapters.Categories{Cache: p.cache}, Threshold: p.acceptThreshold, Locks: p.locksFor("adult"),
	}
}

// comicRecords is the comic service's view of the active definitions; a nil provider must not become a non-nil interface.
func (p *process) comicRecords(key string) comic.RecordProvider {
	provider := p.records.get(key)
	if provider == nil {
		return nil
	}
	return provider
}

// movieRecords is the movie service's view of the active definitions.
func (p *process) movieRecords(key string) movie.RecordProvider {
	provider := p.records.get(key)
	if provider == nil {
		return nil
	}
	return provider
}

// tvRecords is the TV service's view of the active definitions.
func (p *process) tvRecords(key string) tv.RecordProvider {
	provider := p.records.get(key)
	if provider == nil {
		return nil
	}
	return provider
}

// bookRecords is the book service's view of the active definitions.
func (p *process) bookRecords(key string) book.RecordProvider {
	provider := p.records.get(key)
	if provider == nil {
		return nil
	}
	return provider
}

// comicService is the comic service over the six providers, each on its own bucket and pace.
func (p *process) comicService() comic.Service {
	client := func(name string) *httpx.Client { return p.providerClient(name, nil, httpx.ClientConfig{}) }
	metronKey := p.providerKey(settings.ComicSourceMetron)
	return comic.Service{
		Sources: comic.Sources{
			ComicVine:    comicvine.New(client(settings.ComicSourceComicVine), p.providerKey(settings.ComicSourceComicVine)),
			Metron:       metron.New(client(settings.ComicSourceMetron), func() (string, string) { user, password, _ := strings.Cut(metronKey(), ":"); return user, password }),
			GCD:          gcd.New(p.providerClient(settings.ComicSourceGCD, nil, httpx.ClientConfig{Timeout: 120 * time.Second})), // comics.org answers a title search in up to a minute
			AniList:      anilist.New(client(providerAniList)),
			MangaUpdates: mangaupdates.New(client(settings.ComicSourceMangaUpdates)),
			MangaDex:     mangadex.New(client(settings.ComicSourceMangaDex)),
			Wikidata:     p.wikidataFetcher(),
			Records:      p.comicRecords,
			Local:        p.localMetadata(),
			Logger:       p.logger,
		},
		KindOf: func(categoryID int64) string {
			category, found := p.cache.Snapshot().Category(categoryID)
			if !found {
				return comic.KindComics
			}
			return category.Kind
		},
		ProvidersOf: func(categoryID int64) []string {
			snapshot := p.cache.Snapshot()
			category, found := snapshot.Category(categoryID)
			if !found {
				return nil
			}
			return snapshot.MetadataSourcesFor(category)
		},
		OptionOf: func(categoryID int64, key, fallback string) string {
			category, found := p.cache.Snapshot().Category(categoryID)
			if !found {
				return fallback
			}
			return category.FileManagement.ModuleOption(key, fallback)
		},
		Series: library.NewSeries(p.library), Exclusions: library.NewExclusions(p.library), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library),
		Clock: clock.NewSystem(),
		ProfileFor: func(categoryID int64) int64 {
			category, found := p.cache.Snapshot().Category(categoryID)
			if !found {
				return p.defaultQualityProfileID
			}
			return adapters.Categories{Cache: p.cache}.ProfileFor(category, p.defaultQualityProfileID)
		},
	}
}

// comicGrab builds the comic type's automatic search over the real stores.
func (p *process) comicGrab() comic.Grab {
	blocklist, sightings, filters := library.NewBlocklistStore(p.library), library.NewSightings(p.library), library.NewReleaseFilters(p.library)
	return comic.Grab{
		Search: comic.Search{
			Reader: library.NewSeriesReader(p.library), KindOf: p.comicService().KindOf, OptionOf: p.comicService().OptionOf,
			Deps: works.Deps{Desired: library.NewDesired(p.library), Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist, Sightings: &sightings, ReleaseFilters: &filters},
		},
		Automatic: dispatch.Automatic{
			Dispatch: p.dispatchFor(comic.MediaType), Name: comic.MediaType,
			FreeSpace: func(categoryID int64) (int64, bool) { return p.cache.Snapshot().AcquisitionHeadroom(categoryID) },
		},
	}
}

// comicResolver finds or adds the series a file or a download names.
func (p *process) comicResolver() comic.Resolver {
	return comic.Resolver{
		Service: p.comicService(), Logger: p.logger, Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID,
		Locks: p.locksFor("comic"), Threshold: p.acceptThreshold,
		Languages: func(categoryID int64) []string {
			category, found := p.cache.Snapshot().Category(categoryID)
			return settings.LanguageProfileFor(p.cache.Snapshot().DefaultLanguages(), found && category.Kind == "manga").AudioPreferred
		},
	}
}

// comicCatalog is the comic type's add over the providers, as an import adds a series.
func (p *process) comicCatalog() comic.Catalog {
	return comic.Catalog{Service: p.comicService(), Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID}
}

// comicModule wires the comic type: the add and refresh over the providers and the jobs that keep its metadata current.
func (p *process) comicModule() comic.Module {
	service := p.comicService()
	catalog := comic.Catalog{Service: service, Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID}
	resolver := p.comicResolver()
	grab := p.comicGrab()
	conns := p.comicIndexerConns
	readerFiles := &comic.ReaderFiles{
		FS: p.fsys, Reader: library.NewSeriesReader(p.library), Roots: adapters.Roots{Cache: p.cache}, Clock: clock.NewSystem(), Logger: p.logger,
		KindOf: service.KindOf, OptionOf: service.OptionOf, Rehash: p.rehashFile, KeepOld: p.recycleRewritten,
		RootsOf: func(categoryID int64) []settings.RootFolder {
			return p.cache.Snapshot().RootFoldersForCategory(categoryID)
		},
	}
	var follow *comic.Following
	if creators, ok := service.ComicVine.(comic.CreatorSource); ok {
		follow = &comic.Following{Source: creators, People: library.NewPeople(p.library), Catalog: catalog}
	}
	return comic.Module{
		Feeds:       mangafeeds.Feeds{Charts: p.aniListClient(), Kind: comic.AddKindSeries, Provider: comic.ProviderAniListManga, Clock: clock.NewSystem()},
		Follow:      follow,
		ReaderFiles: readerFiles,
		Rename:      &comic.RenameConfig{Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem()},
		Feed: comic.Feed{
			Grab: grab, Reader: library.NewSeriesReader(p.library), Indexers: conns, Logger: p.logger, Libraries: p.libraryIDsOf(comic.MediaType),
		},
		CutoffJob: comic.CutoffJob{
			Desired: library.NewDesired(p.library), Reader: library.NewSeriesReader(p.library), Settings: p.cache, Recycle: p.recycleSuperseded, Logger: p.logger,
		},
		Searching: comic.NewSearching(grab, conns, clock.NewSystem()),
		Files: comic.Files{
			Resolver: resolver, FS: p.fsys, Unidentified: library.NewUnidentified(p.library), Physical: library.NewPhysical(p.library), Clock: clock.NewSystem(),
			AllowAgain: catalog.AllowAgain, RootPath: func(id int64) (string, bool) {
				root, found := p.cache.Snapshot().RootFolder(id)
				return root.Path, found
			},
		},
		Sweep: scan.Sweep{
			Consumer: comic.NewConsumer(comic.ConsumerConfig{
				FS: p.fsys, Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library), Service: service,
				Clock: clock.NewSystem(), Logger: p.logger, Resolver: resolver,
			}),
			Include: comic.Includes, Probe: func(string) bool { return false },
			Housekeeping: housekeeping.Sweep{
				Categories: adapters.Categories{Cache: p.cache}, Desired: library.NewDesired(p.library),
				Grain: comic.Module{}.Descriptor().WantedGrain, FS: p.fsys, Logger: p.logger,
			},
			OnSwept: p.afterSweep,
		},
		Registrations: []media.JobRegistration{
			{Definition: comic.RefreshDefinition(), Handler: comic.NewRefreshJob(library.NewSeriesReader(p.library), service, clock.NewSystem(), p.logger)},
			{Definition: comic.ArtworkDefinition(), Handler: comic.NewArtworkJob(library.NewSeriesReader(p.library), p.remoteImages(), p.logger)},
			{Definition: comic.CoverDefinition, Handler: comic.NewCoverJob(comic.CoverJobConfig{
				Artworks: library.NewArtworks(p.library), Pictures: library.NewWorks(p.library), Roots: adapters.Roots{Cache: p.cache},
				FS: p.fsys, ImagesDir: p.config.Paths.MediaCoverDir, Clock: clock.NewSystem(), Logger: p.logger,
			})},
			{Definition: comic.ReaderFilesDefinition, Handler: comic.ReaderFilesJob{
				Writer:    *readerFiles,
				Libraries: func() []settings.Category { return p.cache.Snapshot().Categories() },
				RootsOf: func(categoryID int64) []settings.RootFolder {
					return p.cache.Snapshot().RootFoldersForCategory(categoryID)
				},
				Logger: p.logger,
			}},
		},
		Service: service,
		Passes: media.Passes{
			WorksPass: func(ctx context.Context, _ bool) (bool, error) {
				result, err := comic.EnqueueRefresh(ctx, p.queue)
				return result.Duplicate, err
			},
			PicturesPass: func(ctx context.Context, _ bool) (bool, error) {
				if _, err := comic.EnqueueCovers(ctx, p.queue); err != nil {
					p.logger.Warn("enqueue comic covers from files", slog.Any("error", err))
				}
				result, err := comic.EnqueueArtwork(ctx, p.queue)
				return result.Duplicate, err
			},
		},
		Wiring: media.Wiring{
			Adder:    catalog,
			Pictures: adapters.ProviderPictures{Reader: library.NewSeriesReader(p.library), Series: library.NewSeries(p.library), Clock: clock.NewSystem()},
			Hunt: func(ctx context.Context, itemID int64) (plan.Outcome, error) {
				found, err := conns(ctx)
				if err != nil {
					return plan.OutcomeSearched, err
				}
				result, err := grab.SearchAndSend(ctx, itemID, found, dispatch.GrabAutomatic, clock.NewSystem().Now())
				switch {
				case errors.Is(err, comic.ErrNotSearchable):
					return plan.OutcomeNotWanted, nil
				case err != nil:
					return plan.OutcomeSearched, err
				case result.AlreadyInFlight:
					return plan.OutcomeInFlight, nil
				case result.Search.Answered == 0:
					return plan.OutcomeNoAnswer, nil
				}
				return plan.OutcomeSearched, nil
			},
			Refresh: func(ctx context.Context, workID int64) (bool, error) {
				libraries, err := p.workLibraries(ctx, workID)
				if err != nil {
					return false, err
				}
				if len(libraries) == 0 {
					return false, fmt.Errorf("%s %d is in no library", comic.MediaType, workID)
				}
				return false, catalog.Refresh(ctx, libraries[0], workID)
			},
		},
	}
}

// musicServerAsker gives music's scan the MusicBrainz ids a media server holds for the album folder of a file.
type musicServerAsker struct{ asker *localAsker }

// AlbumIDsByPath implements music.ServerAsker; a server that cannot find the path, or holds no ids, answers none.
func (m musicServerAsker) AlbumIDsByPath(ctx context.Context, categoryID int64, absPath string) (music.ServerAlbumIDs, bool) {
	item, found, err := m.asker.AskByPath(ctx, categoryID, absPath)
	if err != nil || !found || item.MediaType != "music" {
		return music.ServerAlbumIDs{}, false
	}
	ids := music.ServerAlbumIDs{ReleaseGroup: item.IDs["musicbrainzreleasegroup"], Release: item.IDs["musicbrainzalbum"]}
	return ids, ids != (music.ServerAlbumIDs{})
}

// adultRecords is the adult service's view of the active definitions.
func (p *process) adultRecords(key string) adult.RecordProvider {
	provider := p.records.get(key)
	if provider == nil {
		return nil
	}
	return provider
}

// workLibraries answers the libraries that want a work, else those of the root folders it lies on, so a scanned work refreshes too.
func (p *process) workLibraries(ctx context.Context, workID int64) ([]int64, error) {
	roots, libraries, err := library.NewWorks(p.library).Places(ctx, workID)
	if err != nil || len(libraries) > 0 {
		return libraries, err
	}
	snapshot := p.cache.Snapshot()
	for _, root := range roots {
		for _, id := range snapshot.CategoriesForRootFolder(root) {
			if !slices.Contains(libraries, id) {
				libraries = append(libraries, id)
			}
		}
	}
	return libraries, nil
}

// tmdbFeeds is the recommendation feeds of film (tmdb.MediaMovie) or TV (tmdb.MediaTV) libraries, read from TMDB's charts.
func (p *process) tmdbFeeds(kind string) media.Recommending {
	p.tvService()
	feeds := tmdbfeeds.Feeds{
		Media: kind, Charts: p.tmdbShared, Clock: clock.NewSystem(), Configured: func() bool { return p.cache.Snapshot().TMDBConfigured() },
	}
	if kind == tmdb.MediaTV {
		feeds.Detail = func(ctx context.Context, id int64) (tmdbfeeds.Detail, error) {
			series, err := p.tmdbShared.Series(ctx, id)
			if err != nil {
				return tmdbfeeds.Detail{}, err
			}
			detail := tmdbfeeds.Detail{Certification: series.ContentRating("US"), TVDBID: series.ExternalIDs.TVDB}
			for _, network := range series.Networks {
				detail.Networks = append(detail.Networks, network.Name)
			}
			return detail, nil
		}
		anime := feeds
		anime.Anime, anime.TVDBProvider = true, tv.ProviderTVDB
		return seriesFeeds{
			series: feeds,
			anime:  media.JoinedFeeds{anifeeds.Feeds{Charts: p.aniListClient(), Resolve: p.resolveSeriesEntry, Clock: clock.NewSystem()}, anime},
			isAnime: func(categoryID int64) bool {
				category, found := p.cache.Snapshot().Category(categoryID)
				return found && category.Kind == "anime"
			},
		}
	}
	feeds.Detail = func(ctx context.Context, id int64) (tmdbfeeds.Detail, error) {
		film, err := p.tmdbShared.Movie(ctx, id)
		if err != nil {
			return tmdbfeeds.Detail{}, err
		}
		detail := tmdbfeeds.Detail{Certification: film.Certification("US")}
		for _, company := range film.Companies {
			detail.Networks = append(detail.Networks, company.Name)
		}
		return detail, nil
	}
	return feeds
}

// musicFeeds is the recommendation feeds of music libraries: Last.fm, ListenBrainz and Deezer side by side.
func (p *process) musicFeeds() media.Recommending {
	names := adapters.MusicNames{Provider: p.musicBrainzProvider()}
	return media.JoinedFeeds{
		p.lastFMFeeds(),
		lbfeeds.Feeds{
			Charts:         listenbrainz.New(p.providerClient(providerListenBrainz, nil, httpx.ClientConfig{})),
			ArtistProvider: music.ProviderArtist, AlbumProvider: music.ProviderReleaseGroup,
		},
		deezerfeeds.Feeds{
			Charts: deezer.New(p.providerClient(providerDeezer, nil, httpx.ClientConfig{})), Names: names,
			ArtistProvider: music.ProviderArtist, AlbumProvider: music.ProviderReleaseGroup,
		},
	}
}

// lastFMFeeds is the recommendation feeds of music libraries, from Last.fm's charts and the library's own artists and genres.
func (p *process) lastFMFeeds() media.Recommending {
	key := p.providerKey(settings.SourceLastFM)
	return lastfmfeeds.Feeds{
		Charts:         lastfm.New(p.providerClient(providerLastFM, nil, httpx.ClientConfig{}), key),
		Names:          adapters.MusicNames{Provider: p.musicBrainzProvider()},
		Taste:          musicTaste{pool: library.NewRecommendations(p.library)},
		ArtistProvider: music.ProviderArtist, AlbumProvider: music.ProviderReleaseGroup, Clock: clock.NewSystem(),
		Configured: func() bool { return key() != "" },
	}
}

type musicTaste struct{ pool library.Recommendations }

func (t musicTaste) Artists(ctx context.Context, categoryID int64, limit int) ([]string, error) {
	return t.pool.TopHeldArtists(ctx, categoryID, int64(limit))
}

func (t musicTaste) Genres(ctx context.Context, categoryID int64, limit int) ([]string, error) {
	return t.pool.TopGenres(ctx, categoryID, int64(limit))
}

// adultFeeds is the recommendation feeds of adult libraries: the trending and upcoming scenes of every stash-box server.
func (p *process) adultFeeds() media.Recommending {
	servers := p.adultService().Servers
	var joined media.JoinedFeeds
	for _, provider := range []string{adult.ProviderStashDB, adult.ProviderThePornDB, adult.ProviderFansDB} {
		server, ok := servers[provider].(adultfeeds.Charts)
		if !ok {
			continue
		}
		label := provider
		if known, found := settings.MetadataProviderByKey(provider); found {
			label = known.Label
		}
		joined = append(joined, adultfeeds.Feeds{Charts: server, Kind: adult.MediaType, Provider: provider, Source: label, Clock: clock.NewSystem()})
	}
	if len(joined) == 0 {
		return nil
	}
	return joined
}

// bookFeeds is the recommendation feeds of a book library: Open Library's and Hardcover's charts, with Audible's catalogue first for audiobooks.
func (p *process) bookFeeds(format book.Format) media.Recommending {
	openLibrary := openlibrary.New(p.providerClient(providerOpenLibrary, nil, httpx.ClientConfig{}))
	joined := media.JoinedFeeds{
		olfeeds.Feeds{Charts: openLibrary, Kind: format.MediaType, Provider: format.Provider(book.ProviderOpenLibrary), Clock: clock.NewSystem()},
		hcfeeds.Feeds{
			Charts: hardcover.New(p.providerClient(providerHardcover, nil, httpx.ClientConfig{}), p.providerKey(settings.BookSourceHardcover)), Kind: format.MediaType,
			WorkProvider: format.Provider(book.ProviderOpenLibrary), HardcoverProvider: format.Provider(book.ProviderHardcover), Clock: clock.NewSystem(),
			Resolve: func(ctx context.Context, isbn string) (string, bool, error) {
				edition, err := openLibrary.ByISBN(ctx, isbn)
				if errors.Is(err, openlibrary.ErrNotFound) {
					return "", false, nil
				}
				return edition.WorkID(), err == nil && edition.WorkID() != "", err
			},
		},
	}
	if format.ByFolder {
		client := p.providerClient(providerAudible, nil, httpx.ClientConfig{})
		return append(media.JoinedFeeds{audiblefeeds.Feeds{Charts: audible.New(client, client, nil), Kind: format.MediaType, Clock: clock.NewSystem()}}, joined...)
	}
	return joined
}

// hardcoverSeries lists a series' books from Hardcover, which answers nothing until a key is stored.
type hardcoverSeries struct{ client hardcover.Client }

// ListSeries implements book.SeriesLister.
func (h hardcoverSeries) ListSeries(ctx context.Context, name string) ([]book.ListedBook, error) {
	found, err := h.client.SeriesBooks(ctx, name)
	listed := make([]book.ListedBook, len(found))
	for index, one := range found {
		listed[index] = book.ListedBook{ID: strconv.FormatInt(one.ID, 10), Title: one.Title, Year: one.Year, Position: one.Position, Language: one.Language}
	}
	return listed, err
}
