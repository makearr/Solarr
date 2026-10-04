package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// statisticsJobType rebuilds stat_rollup (section 21.12): the statistics page reads it and runs no aggregate itself.
const statisticsJobType = jobs.Type("library.statistics")

var statisticsDefinition = jobs.Definition{
	Name: "Computing statistics",
	Type: statisticsJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 2,
}

func (p *process) registerStatisticsJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(statisticsDefinition); err != nil {
		return err
	}
	statistics := library.NewStatistics(p.library)
	executor.Handle(statisticsJobType, jobs.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		categoryOfRoot := make(map[int64]int64)
		for _, attached := range p.cache.Snapshot().CategoryRoots() {
			categoryOfRoot[attached.RootFolderID] = attached.CategoryID
		}
		if err := statistics.Compute(ctx, categoryOfRoot, clock.NewSystem().Now().Unix()); err != nil {
			return fmt.Errorf("statistics: %w", err)
		}
		p.logger.Info("statistics computed", slog.Int("roots", len(categoryOfRoot)))
		return nil
	}))
	return nil
}

func (p *process) enqueueStatistics(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: statisticsJobType, ActionKey: string(statisticsJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
