package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

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
		Fingerprints: tool,
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
	job := scan.NewJob(scan.Config{
		FS:       p.fsys,
		Scans:    library.NewScans(p.library),
		Roots:    adapters.Roots{Cache: p.cache},
		Tool:     tool,
		Consumer: consumer,
		Physical: physical,
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
		Include:  scan.IsAudio,
		Housekeeping: music.NewHousekeeping(music.HousekeepingConfig{
			FS: p.fsys, Categories: adapters.Categories{Cache: p.cache}, Roots: adapters.DrainRoots{Cache: p.cache},
			Affinity: p.cache, Desired: &desired, Subscriptions: library.NewSubscriptions(p.library),
			People: library.NewPeople(p.library), GroupID: lookupGroupID,
		}),
		// Enrichment follows at once, not at the next hourly run: a new library had no tracklists,
		// photos or Wanted for up to an hour (found 2026-09-28).
		OnSwept: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx); _, _ = p.enqueueEnrichment(ctx) },
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

// enqueueLibraryScans submits an incremental sweep for every online root
// folder attached to a music category. An incremental sweep of a root that
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

// enqueueScans fans out to several jobs, one per online music root, unlike
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
		if category.MediaType != "music" {
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
