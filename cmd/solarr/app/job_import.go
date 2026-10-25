package app

import (
	"context"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// registerImportJob wires ADR-48's import.inspect job: each media type imports a batch its own way, and a session
// with no library yet is identified as music, which is what an unsorted download folder mostly is.
func (p *process) registerImportJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(importjob.Definition); err != nil {
		return err
	}
	job := importjob.NewJob(importjob.Config{
		FS:         p.fsys,
		Sessions:   library.NewImportSessions(p.library),
		Policy:     adapters.ImportPolicy{Cache: p.cache},
		Packs:      library.NewPacks(p.library),
		Importers:  p.batchImporters(),
		Unassigned: string(music.MediaType),
		MediaTypeOf: func(categoryID int64) string {
			if category, found := p.cache.Snapshot().Category(categoryID); found {
				return category.MediaType
			}
			return ""
		},
		Workspace: filepath.Join(p.config.Paths.CacheDir, "import-workspace"),
		Events:    p.bus,
		// A new edition's tracklist is what lets the placed files count towards the album.
		OnCommitted: func(ctx context.Context) {
			_, _ = p.enrichmentPass(ctx, false)
			_, _ = p.enqueueCategoryCounts(ctx)
		},
		Clock:  clock.NewSystem(),
		Logger: p.logger,
	})
	executor.Handle(importjob.JobType, job)
	if err := registry.Register(importjob.RetryDefinition); err != nil {
		return err
	}
	executor.Handle(importjob.RetryJobType, importjob.NewRetryJob(library.NewImportSessions(p.library), p.manualCommitter(), p.logger))
	return nil
}

// importRetryTask is the registered sweep that places what a library could not take when it was imported.
func (p *process) importRetryTask() jobs.Task {
	return jobs.Task{
		Key: importjob.RetryScheduleKey, Name: "Retry imports a library could not take", DefaultCadence: importRetryScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryDownloads, Startup: jobs.StartupOn,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) { return importjob.EnqueueRetry(ctx, p.queue) },
	}
}

// acquisitionReleaseGroups are the release groups of the album a download was grabbed for.
func (p *process) acquisitionReleaseGroups(ctx context.Context, acquisitionID int64) ([]string, error) {
	acquisition, err := library.NewAcquisitions(p.library).Get(ctx, acquisitionID)
	if err != nil || acquisition.DesiredItemID == nil {
		return nil, err
	}
	item, err := library.NewDesired(p.library).Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		return nil, err
	}
	detail, err := library.NewWorks(p.library).Detail(ctx, item.WorkID)
	if err != nil {
		return nil, err
	}
	var groups []string
	for _, external := range detail.ExternalIDs {
		if external.Provider == music.ProviderReleaseGroup {
			groups = append(groups, external.ExternalID)
		}
	}
	return groups, nil
}

// importCommitter builds ADR-48's own COMMIT step. A fresh instance each
// call, the same "ask, don't cache" style p.musicBrainzProvider() already
// uses -- every field it closes over is itself cheap to construct or already
// long-lived (p.cache, p.library). Shared by the import.inspect job's own
// inline WALK-time placement and manualCommitter's own ManualCommitter below,
// so the two never drift apart on how a unit gets placed.
func (p *process) importCommitter() music.Committer {
	desired := library.NewDesired(p.library)
	people := library.NewPeople(p.library)
	return music.NewCommitter(music.CommitterConfig{
		FS:               p.fsys,
		Roots:            adapters.DrainRoots{Cache: p.cache},
		Affinity:         p.cache,
		Physical:         library.NewPhysical(p.library),
		Identity:         library.NewIdentity(p.library),
		Desired:          &desired,
		Categories:       adapters.Categories{Cache: p.cache},
		BlockedContent:   library.NewBlockedContentStore(p.library),
		Clock:            clock.NewSystem(),
		DefaultProfileID: p.defaultQualityProfileID,
		TransferLadder:   p.transferLadder,
		RenameOnImport:   p.renameOnImport,
		ExtraFiles:       p.extraFileExtensions,
		GroupID:          lookupGroupID,
		People:           &people,
	})
}

// transferLadder reads the configured order per commit, so changing it in the
// interface takes effect on the next import rather than the next restart.
func (p *process) transferLadder() []mover.PlaceMethod {
	configured := p.cache.Snapshot().ImportTransferLadder()
	ladder := make([]mover.PlaceMethod, 0, len(configured))
	for _, method := range configured {
		ladder = append(ladder, mover.PlaceMethod(method))
	}
	return ladder
}

// renameOnImport reads the switch per commit, the same live read the ladder
// above is.
func (p *process) renameOnImport() bool {
	return p.cache.Snapshot().ImportRenameOnImport()
}

// extraFileExtensions answers the extensions to bring along into a library, or none while its switch is off.
func (p *process) extraFileExtensions(categoryID int64) []string {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found {
		return nil
	}
	return adapters.MediaTypes{Registry: p.media}.ExtraFileExtensions(category)
}

// lookupGroupID resolves a library's "chown group" name in this container's own group database.
func lookupGroupID(name string) (int, error) {
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(group.Gid)
}

// mediaTool builds the ffprobe reader every import path needs -- the same
// mediatool.New(nil, 0) registerImportJob already used, named so
// manualCommitter can build the identical instance without importing
// mediatool.New's own zero-value convention twice.
func (p *process) mediaTool() mediatool.Tool {
	return mediatool.New(nil, 0)
}

// manualCommitter is the Import screen's commit-what-was-held and place-once-assigned, through each type's importer.
func (p *process) manualCommitter() importjob.ManualCommitter {
	importers := p.batchImporters()
	return importjob.NewManualCommitter(p.fsys, library.NewImportSessions(p.library), library.NewPacks(p.library),
		func(categoryID int64) batch.Importer {
			category, found := p.cache.Snapshot().Category(categoryID)
			if !found {
				return nil
			}
			return importers[category.MediaType]
		})
}

// batchImporters are each media type's own identify-and-place of an import, by type.
func (p *process) batchImporters() map[string]batch.Importer {
	return map[string]batch.Importer{
		string(music.MediaType): p.musicImporter(), tv.MediaType: p.tvImporter(), movie.MediaType: p.movieImporter(),
	}
}

// musicImporter is Lidarr's import: tags, MusicBrainz search, album matching and fingerprints, placed by importCommitter.
func (p *process) musicImporter() music.Importer {
	return music.NewImporter(music.ImporterConfig{
		Tool: p.mediaTool(), Search: p.musicBrainzProvider(), Albums: p.musicBrainzProvider(), AcoustID: p.acoustIDLookup(),
		Committer: p.importCommitter(), Provenance: p.acquisitionReleaseGroups, Logger: p.logger,
	})
}

// tvImporter is the TV module's batch importer: the same placement rules as music's commit, naming by the library's TV
// profile.
func (p *process) tvImporter() tv.Importer {
	categories := adapters.Categories{Cache: p.cache}
	service := p.tvService()
	return tv.NewImporter(tv.ImporterConfig{
		FS: p.fsys, Roots: adapters.DrainRoots{Cache: p.cache}, Affinity: p.cache, Physical: library.NewPhysical(p.library),
		Series: service.Series, Reader: library.NewSeriesReader(p.library), Identity: library.NewIdentity(p.library),
		Desired: library.NewDesired(p.library), Acquisitions: library.NewAcquisitions(p.library),
		Resolver: tv.Resolver{
			Service: service, Reader: library.NewSeriesReader(p.library), Clock: clock.NewSystem(), Logger: p.logger,
			DefaultProfileID: p.defaultQualityProfileID, Categories: categories,
		},
		Categories: categories, BlockedContent: library.NewBlockedContentStore(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		Exclusions:     adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)},
		TransferLadder: p.transferLadder, RenameOnImport: p.renameOnImport, GroupID: lookupGroupID, BelowCutoff: p.belowCutoff,
		ExtraFiles: p.extraFileExtensions, SharedFiles: p.enqueueSharedFiles, AfterPlacing: p.enqueueServerFilesFor, Probe: p.probeMeasured,
	})
}

// movieImporter is the movie module's batch importer, placing through the same steps as TV's.
func (p *process) movieImporter() movie.Importer {
	categories := adapters.Categories{Cache: p.cache}
	identity := library.NewIdentity(p.library)
	return movie.NewImporter(movie.ImporterConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Series: library.NewSeries(p.library),
		Reader: library.NewSeriesReader(p.library), Identity: identity, Desired: library.NewDesired(p.library),
		Acquisitions: library.NewAcquisitions(p.library),
		Placer: placer.New(placer.Config{
			FS: p.fsys, Roots: adapters.DrainRoots{Cache: p.cache}, Affinity: p.cache, Identity: identity,
			Blocked: library.NewBlockedContentStore(p.library), Clock: clock.NewSystem(), Logger: p.logger,
			TransferLadder: p.transferLadder, GroupID: lookupGroupID,
		}),
		Resolver: movie.Resolver{
			Service: p.movieService(), Reader: library.NewSeriesReader(p.library), Logger: p.logger,
			DefaultProfileID: p.defaultQualityProfileID, Categories: categories, Locks: &keyedlock.Locks{},
		},
		Categories: categories, Clock: clock.NewSystem(), Logger: p.logger,
		Exclusions:     adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)},
		RenameOnImport: p.renameOnImport, ExtraFiles: p.extraFileExtensions, Probe: p.probeMeasured,
		AfterPlacing: p.enqueueMovieServerFilesFor,
	})
}

// belowCutoff says a measured episode file misses its library's cutoff.
func (p *process) belowCutoff(categoryID int64, file library.EpisodeFile) bool {
	snapshot := p.cache.Snapshot()
	category, found := snapshot.Category(categoryID)
	if !found || file.VideoCodec == "" {
		return false
	}
	judged, ok := quality.Judge(snapshot, category, tv.FileFacts(file))
	return ok && !judged.MeetsCutoff
}

// probeMeasured is what ffprobe measured of one file, for the import rules (sample by runtime, no audio track).
func (p *process) probeMeasured(ctx context.Context, absolutePath string) (mediatool.Measured, bool) {
	probes, err := p.tool.ReadTags(ctx, []string{absolutePath})
	if err != nil || len(probes) != 1 || probes[0].Err != nil {
		return mediatool.Measured{}, false
	}
	return probes[0].Measured, true
}
