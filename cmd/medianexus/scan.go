package main

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/scan"
	"github.com/makearr/MediaNexus/internal/settings"
)

// cacheRoots adapts the ADR-25 settings snapshot to scan.Roots: it asks the cache
// fresh on every lookup rather than holding a copy that could go stale underneath it.
type cacheRoots struct{ cache *settings.Cache }

func (c cacheRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	return c.cache.Snapshot().RootFolder(id)
}

// registerScanJob wires the first job handler in the codebase into the registry and
// the executor: the library sweep, its music consumer, and the tools it probes files
// with. The registry entry is required before Enqueue will accept the type at all
// (Queue.Enqueue looks the definition up to route it).
func (p *process) registerScanJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(scan.Definition); err != nil {
		return err
	}
	physical := library.NewPhysical(p.library)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS:           p.fsys,
		Physical:     physical,
		Identity:     library.NewIdentity(p.library),
		Unidentified: library.NewUnidentified(p.library),
		Clock:        clock.NewSystem(),
		Logger:       p.logger,
	})
	job := scan.NewJob(scan.Config{
		FS:       p.fsys,
		Scans:    library.NewScans(p.library),
		Roots:    cacheRoots{cache: p.cache},
		Tool:     mediatool.New(nil, 0),
		Consumer: consumer,
		Physical: physical,
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
		Include:  scan.IsAudio,
	})
	executor.Handle(scan.JobType, job)
	return nil
}

// enqueueLibraryScans submits an incremental sweep for every online root folder
// attached to a music category, once at startup. There is no scheduler yet -- stage 6
// builds the periodic trigger -- so this is what makes the library browsable at all
// meanwhile, and an incremental sweep of an unswept root reads everything regardless.
func (p *process) enqueueLibraryScans(ctx context.Context) {
	snapshot := p.cache.Snapshot()
	for _, category := range snapshot.Categories() {
		if category.MediaType != "music" {
			continue
		}
		for _, root := range snapshot.RootFoldersForCategory(category.ID) {
			if !root.Online {
				continue
			}
			if _, err := scan.Enqueue(ctx, p.queue, root.ID, false); err != nil {
				p.logger.Warn("enqueue library scan",
					slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
			}
		}
	}
}
