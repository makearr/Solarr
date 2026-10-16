package app

import (
	"context"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// registerImportJob wires ADR-48's import.inspect job into the registry and
// the executor. There is no enqueue trigger yet -- import.inspect is
// user-triggered (point it at a path), not a periodic sweep like a scan or a
// probe, so the natural trigger is the still-unbuilt POST /api/v1/import/
// inspect endpoint (next.md's own next item), not a startup call. Registered
// now anyway, the same ready-ahead-of-consumer shape every engine piece this
// session landed already has -- the executor has to know the type exists
// before anything can enqueue one.
//
// The Committer (todo 4.9's "COMMIT actually imports") reuses the exact
// adapters registerScanJob already built for the same purpose --
// adapters.DrainRoots already has the RootFolder/CategoryRoots shape
// music.PlacementRoots needs, structurally, with no new adapter type.
//
// Search wires music.SearchIdentifier into IDENTIFY (todo 4.9's own
// remaining item) -- the same musicbrainz.Provider registerScanJob already
// hands its own Consumer, so a scene release with no embedded MBID can
// still auto-resolve on import, not only on a library scan.
// identifyCluster's 8-wide fan-out (job.go) was built ahead of exactly this;
// httpx's global rate limiter (ADR-13) is what keeps several concurrent
// MusicBrainz lookups from one cluster paced rather than bursting.
func (p *process) registerImportJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(importjob.Definition); err != nil {
		return err
	}
	job := importjob.NewJob(importjob.Config{
		FS:         p.fsys,
		Sessions:   library.NewImportSessions(p.library),
		Policy:     adapters.ImportPolicy{Cache: p.cache},
		Tool:       p.mediaTool(),
		Search:     p.musicBrainzProvider(),
		Albums:     p.musicBrainzProvider(),
		Provenance: p.acquisitionReleaseGroups,
		AcoustID:   p.acoustIDLookup(),
		Committer:  p.importCommitter(),
		Packs:      library.NewPacks(p.library),
		Importers:  p.batchImporters(),
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
	return nil
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

// manualCommitter builds the Import screen's two remaining UI actions
// (manualcommit.go): commit-what-was-held and assign-a-category-later. A
// second Committer instance, not the same value registerImportJob's own job
// holds -- both are cheap, stateless wrappers over the same long-lived
// dependencies, and importjob.Job keeps its own unexported.
func (p *process) manualCommitter() importjob.ManualCommitter {
	importers := p.batchImporters()
	return importjob.NewManualCommitter(
		p.fsys, p.mediaTool(), library.NewImportSessions(p.library), p.importCommitter(),
		library.NewPacks(p.library),
	).WithImporters(func(categoryID int64) batch.Importer {
		category, found := p.cache.Snapshot().Category(categoryID)
		if !found {
			return nil
		}
		return importers[category.MediaType]
	})
}

// batchImporters are the media types that identify and place an import in their own way, by type.
func (p *process) batchImporters() map[string]batch.Importer {
	return map[string]batch.Importer{tv.MediaType: p.tvImporter()}
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
		ExtraFiles: p.extraFileExtensions, SharedFiles: p.enqueueSharedFiles, AfterPlacing: p.enqueueServerFilesFor,
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
