package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
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
	measure, rollup := library.NewStatistics(p.library), statistics.NewRollup(p.statistics)
	executor.Handle(statisticsJobType, jobs.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		categoryOfRoot := make(map[int64]int64)
		for _, attached := range p.cache.Snapshot().CategoryRoots() {
			categoryOfRoot[attached.RootFolderID] = attached.CategoryID
		}
		now := clock.NewSystem().Now().Unix()
		rows, err := measure.Figures(ctx, categoryOfRoot, now)
		if err != nil {
			return fmt.Errorf("statistics: %w", err)
		}
		if err := rollup.Replace(ctx, library.ComputedMetrics, statFigures(rows), now); err != nil {
			return err
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

// statFigures are library.db's measurements as statistics.db stores them.
func statFigures(rows []library.StatRow) []statistics.Figure {
	figures := make([]statistics.Figure, len(rows))
	for i, row := range rows {
		figures[i] = statistics.Figure{Metric: row.Metric, Dimension: row.Dimension, Value: row.Value, Count: row.Count}
	}
	return figures
}

// copyStatRollup brings the figures library.db's rollup held into statistics.db on its first start, so the statistics
// page and the incomplete discographies do not wait a day or a week for the next run.
func (p *process) copyStatRollup(ctx context.Context) error {
	rollup := statistics.NewRollup(p.statistics)
	empty, err := rollup.Empty(ctx)
	if err != nil || !empty {
		return err
	}
	rows, err := library.NewStatistics(p.library).StoredRollup(ctx)
	if err != nil || len(rows) == 0 {
		return err
	}
	byMetric := map[string][]statistics.Figure{}
	computedAt := map[string]int64{}
	for _, row := range rows {
		byMetric[row.Metric] = append(byMetric[row.Metric], statistics.Figure{Metric: row.Metric, Dimension: row.Dimension, Value: row.Value, Count: row.Count})
		computedAt[row.Metric] = max(computedAt[row.Metric], row.ComputedAt)
	}
	for metric, figures := range byMetric {
		if err := rollup.Replace(ctx, []string{metric}, figures, computedAt[metric]); err != nil {
			return err
		}
	}
	p.logger.Info("statistics moved to statistics.db", slog.Int("figures", len(rows)))
	return nil
}
