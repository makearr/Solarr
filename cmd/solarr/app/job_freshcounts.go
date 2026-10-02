package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// freshCountsJobType is the registered type of the fresh-works counter sweep.
const freshCountsJobType = jobs.Type("library.reconcile_fresh_counts")

// freshCountsActionKey deduplicates the sweep: one global figure, the same
// shape categoryCountsActionKey uses for its own sweep.
const freshCountsActionKey = "library.reconcile_fresh_counts"

var freshCountsDefinition = jobs.Definition{
	Type: freshCountsJobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 3,
}

// freshCountsJob recomputes ADR-55's dashboard "new arrivals" figure
// (library.FreshCounts) -- wiring only, so it lives here rather than in
// internal/db/library, which never calls time.Now() itself (forbidigo).
// The cutoff is resolved by adapters.FreshSinceCutoff, the same helper the
// Library list's own fresh=true filter uses, so the dashboard panel and the
// list toggle can never disagree about what "fresh" means.
type freshCountsJob struct {
	counts library.FreshCounts
	clock  clock.Clock
	fresh  adapters.FreshWindow
	logger *slog.Logger
}

func (j freshCountsJob) Run(ctx context.Context, _ jobs.Job) error {
	since, rssSince := j.fresh.Cutoffs(j.clock)

	corrections, err := j.counts.Reconcile(ctx, since, rssSince)
	if err != nil {
		return err
	}
	j.logger.Info("fresh works counter reconciled", slog.Int("corrections", len(corrections)))
	return nil
}

// freshWindow is the instance's configured freshness windows (priority settings).
func (p *process) freshWindow() (days, rssHours int64) {
	stored := p.cache.Snapshot().PrioritySettings()
	return stored.NewReleaseDays, *stored.RSSFreshHours
}

// registerFreshCountsJob wires the sweep into the registry and the executor.
func (p *process) registerFreshCountsJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(freshCountsDefinition); err != nil {
		return err
	}
	job := freshCountsJob{
		counts: library.NewFreshCounts(p.library, clock.NewSystem()),
		clock:  clock.NewSystem(),
		fresh:  p.freshWindow,
		logger: p.logger,
	}
	executor.Handle(freshCountsJobType, job)
	return nil
}

// enqueueFreshCounts submits one sweep. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueFreshCounts(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: freshCountsJobType, ActionKey: freshCountsActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
	if err != nil {
		p.logger.Warn("enqueue fresh counts", slog.Any("error", err))
	}
	return result, err
}
