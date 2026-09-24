package app

import (
	"context"
	"os/user"
	"strconv"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
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
		FS:        p.fsys,
		Sessions:  library.NewImportSessions(p.library),
		Policy:    adapters.ImportPolicy{Cache: p.cache},
		Tool:      p.mediaTool(),
		Search:    p.musicBrainzProvider(),
		Albums:    p.musicBrainzProvider(),
		AcoustID:  p.acoustIDLookup(),
		Committer: p.importCommitter(),
		Packs:     library.NewPacks(p.library),
		Events:    p.bus,
		// A new edition's tracklist is what lets the placed files count towards the album.
		OnCommitted: func(ctx context.Context) {
			_, _ = p.enqueueEnrichment(ctx)
			_, _ = p.enqueueCategoryCounts(ctx)
		},
		Clock:  clock.NewSystem(),
		Logger: p.logger,
	})
	executor.Handle(importjob.JobType, job)
	return nil
}

// importCommitter builds ADR-48's own COMMIT step. A fresh instance each
// call, the same "ask, don't cache" style p.musicBrainzProvider() already
// uses -- every field it closes over is itself cheap to construct or already
// long-lived (p.cache, p.library). Shared by the import.inspect job's own
// inline WALK-time placement and manualCommitter's own ManualCommitter below,
// so the two never drift apart on how a unit gets placed.
func (p *process) importCommitter() music.Committer {
	desired := library.NewDesired(p.library)
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

// extraFileExtensions answers the extensions to bring along, or none while the switch is off.
func (p *process) extraFileExtensions() []string {
	if extras := p.cache.Snapshot().ImportExtraFiles(); extras.Enabled {
		return extras.Extensions
	}
	return nil
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
	return importjob.NewManualCommitter(
		p.fsys, p.mediaTool(), library.NewImportSessions(p.library), p.importCommitter(),
		library.NewPacks(p.library),
	)
}
