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

// cacheCategories adapts the settings snapshot to music.Categories, the same
// "ask fresh, never cache" shape as cacheRoots.
type cacheCategories struct{ cache *settings.Cache }

func (c cacheCategories) Category(id int64) (settings.Category, bool) {
	return c.cache.Snapshot().Category(id)
}

// registerScanJob wires the first job handler in the codebase into the registry and
// the executor: the library sweep, its music consumer, and the tools it probes files
// with. The registry entry is required before Enqueue will accept the type at all
// (Queue.Enqueue looks the definition up to route it).
func (p *process) registerScanJob(ctx context.Context, registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(scan.Definition); err != nil {
		return err
	}
	tool := mediatool.New(nil, 0)
	reportToolReadiness(ctx, tool, p.logger)
	physical := library.NewPhysical(p.library)
	provider := p.musicBrainzProvider()
	desired := library.NewDesired(p.library)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS:           p.fsys,
		Physical:     physical,
		Identity:     library.NewIdentity(p.library),
		Unidentified: library.NewUnidentified(p.library),
		Search:       provider,
		Clock:        clock.NewSystem(),
		Logger:       p.logger,
		Desired:      &desired,
		Categories:   cacheCategories{cache: p.cache},
		// The user's own default when they haven't configured one for the
		// category yet -- ensureDefaultQualityProfile seeded it at startup.
		DefaultProfileID: p.defaultQualityProfileID,
	})
	job := scan.NewJob(scan.Config{
		FS:       p.fsys,
		Scans:    library.NewScans(p.library),
		Roots:    cacheRoots{cache: p.cache},
		Tool:     tool,
		Consumer: consumer,
		Physical: physical,
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
		Include:  scan.IsAudio,
	})
	executor.Handle(scan.JobType, job)
	return nil
}

// reportToolReadiness logs what ADR-30's external tools are missing, once, at
// startup -- the same "reported, never enforced" shape section 20.2 already uses
// for host permissions: every scanned file would otherwise fail its own probe
// individually, which finds the same fact one file at a time instead of once,
// loudly, before the first scan even runs.
func reportToolReadiness(ctx context.Context, tool mediatool.Tool, logger *slog.Logger) {
	if err := tool.Available(ctx); err != nil {
		logger.Warn("media tools unavailable; file identification will fail until they are installed",
			slog.Any("error", err))
	}
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
			if _, err := scan.Enqueue(ctx, p.queue, root.ID, category.ID, false); err != nil {
				p.logger.Warn("enqueue library scan",
					slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
			}
		}
	}
}
