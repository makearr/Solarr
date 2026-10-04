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
	reportToolReadiness(ctx, p.tool, p.logger)
	executor.Handle(scan.JobType, scan.NewJob(scan.Config{
		FS:       p.fsys,
		Scans:    library.NewScans(p.library),
		Roots:    adapters.Roots{Cache: p.cache},
		Tool:     p.tool,
		Sweeps:   adapters.MediaSweeps{Registry: p.media, Cache: p.cache},
		Physical: library.NewPhysical(p.library),
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
	}))
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

// enqueueLibraryScans submits an incremental sweep for every online root
// folder attached to a library whose media type is scanned. An incremental sweep of a root that
// has never been swept reads everything regardless, so this is also what
// makes a freshly attached root browsable. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueLibraryScans(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueScans(ctx, false)
}

// enqueueFullLibraryScans is scan's own jobs.Task.RunFull: every directory is
// re-read regardless of mtime, the one way today to backfill todo 4.2's
// claimed attributes across a library that predates that landing, or more
// generally to catch up a library the incremental watermark has skipped
// (private/questions.md). Manual only -- there is no scheduled cadence for
// it, since re-reading everything on a timer would defeat the point of the
// incremental sweep the ordinary schedule already runs.
func (p *process) enqueueFullLibraryScans(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueScans(ctx, true)
}

// enqueueScans fans out to several jobs, one per online root, unlike
// every other task in this file -- the aggregate EnqueueResult reports
// Duplicate only when every attempted root was already active, and joins
// every root's own error (each still logged individually below) rather than
// reporting only the first, so the scheduler's own recorded outcome does not
// hide a partial failure across several roots.
func (p *process) enqueueScans(ctx context.Context, full bool) (jobs.EnqueueResult, error) {
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
			attempted++
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
