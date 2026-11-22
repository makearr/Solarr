package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// registerScanJob wires the library sweep: one job for every media type, which hands each library's files to the
// sweep its own type declares. The registry entry is required before Enqueue will accept the type at all.
func (p *process) registerScanJob(ctx context.Context, registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(scan.Definition); err != nil {
		return err
	}
	if err := registry.Register(scan.RescanDefinition); err != nil {
		return err
	}
	reportToolReadiness(ctx, p.tool, p.logger)
	handler := scan.NewJob(scan.Config{
		FS:       p.fsys,
		Scans:    library.NewScans(p.library),
		Roots:    adapters.Roots{Cache: p.cache},
		Tool:     p.tool,
		Sweeps:   adapters.MediaSweeps{Registry: p.media, Cache: p.cache},
		Physical: library.NewPhysical(p.library),
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
	})
	executor.Handle(scan.JobType, handler)
	executor.Handle(scan.RescanJobType, handler)
	return p.registerMediaJobs(registry, executor)
}

// registerMediaJobs registers the job types each media type brings of its own.
func (p *process) registerMediaJobs(registry *jobs.Registry, executor *jobs.Executor) error {
	register := func(definition jobs.Definition, handler jobs.Handler) error {
		if err := registry.Register(definition); err != nil {
			return err
		}
		executor.Handle(definition.Type, handler)
		return nil
	}
	for _, descriptor := range p.media.Descriptors() {
		module, ok := media.Capability[media.Jobs](p.media, descriptor.Key)
		if !ok {
			continue
		}
		if err := module.RegisterJobs(register); err != nil {
			return err
		}
	}
	return nil
}

// reportToolReadiness logs once at startup which ADR-30 external tools are missing (reported, never enforced, like section 20.2), instead of every file failing its probe.
func reportToolReadiness(ctx context.Context, tool mediatool.Tool, logger *slog.Logger) {
	if err := tool.Available(ctx); err != nil {
		logger.Warn("media tools unavailable; file identification will fail until they are installed",
			slog.Any("error", err))
	}
}

// enqueueLibraryScans submits an incremental sweep per online root of a scanned library, also scheduled via jobs.TaskRunner (todo 4.19); a never-swept root reads everything, so this makes a new root browsable.
func (p *process) enqueueLibraryScans(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueScans(ctx, false, false, !p.scanStarted.Swap(true))
}

// enqueueManualLibraryScans is "Run now": every root, whatever the schedule thinks it is due.
func (p *process) enqueueManualLibraryScans(ctx context.Context) (jobs.EnqueueResult, error) {
	p.scanStarted.Store(true)
	return p.enqueueScans(ctx, false, true, false)
}

// enqueueFullLibraryScans is scan's RunFull: it re-reads every directory regardless of mtime to backfill claimed attributes or catch up past the incremental watermark; manual only, since a timer would defeat the incremental sweep.
func (p *process) enqueueFullLibraryScans(ctx context.Context) (jobs.EnqueueResult, error) {
	p.scanStarted.Store(true)
	return p.enqueueScans(ctx, true, true, false)
}

// enqueueScans fans out one job per online root: the aggregate reports Duplicate only when every root was active and joins every root's error, so a partial failure is not hidden.
func (p *process) enqueueScans(ctx context.Context, full, manual, startup bool) (jobs.EnqueueResult, error) {
	snapshot := p.cache.Snapshot()
	var errs []error
	attempted, duplicates := 0, 0
	for _, category := range snapshot.Categories() {
		if _, scanned := media.Capability[media.Scanning](p.media, media.Key(category.MediaType)); !scanned {
			continue
		}
		for _, root := range snapshot.RootFoldersForCategory(category.ID) {
			if !root.Online {
				continue
			}
			if decision := p.scanDecision(ctx, root.ID, manual, startup, true); !decision.Due {
				p.logger.Debug("library scan not due", slog.Int64("root_folder_id", root.ID), slog.String("why", string(decision.Why)))
				continue
			}
			attempted++
			p.rememberFingerprint(ctx, root)
			result, err := scan.Enqueue(ctx, p.queue, root.ID, category.ID, full)
			if err != nil {
				p.logger.Warn("enqueue library scan",
					slog.Int64("root_folder_id", root.ID), slog.Bool("full", full), slog.Any("error", err))
				errs = append(errs, err)
				continue
			}
			if result.Duplicate {
				duplicates++
			}
		}
	}
	if len(errs) > 0 {
		return jobs.EnqueueResult{}, errors.Join(errs...)
	}
	return jobs.EnqueueResult{Duplicate: attempted > 0 && duplicates == attempted}, nil
}
