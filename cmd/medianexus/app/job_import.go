package app

import (
	"github.com/makearr/MediaNexus/cmd/medianexus/adapters"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/importing/importjob"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
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
		Committer: p.importCommitter(),
		Packs:     library.NewPacks(p.library),
		Events:    p.bus,
		Clock:     clock.NewSystem(),
		Logger:    p.logger,
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
	})
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
