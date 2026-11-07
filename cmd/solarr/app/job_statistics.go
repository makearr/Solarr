package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

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
	decisions := statistics.NewCandidates(p.statistics)
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
		decided, err := decisions.DecisionsByOutcome(ctx, now-library.StatisticsWindowDays*86_400)
		if err != nil {
			return fmt.Errorf("statistics: %w", err)
		}
		searched, err := decisions.SearchesByCategory(ctx, now-library.StatisticsWindowDays*86_400)
		if err != nil {
			return fmt.Errorf("statistics: %w", err)
		}
		byIndexer, err := decisions.HistoryByIndexer(ctx, now-library.StatisticsWindowDays*86_400)
		if err != nil {
			return fmt.Errorf("statistics: %w", err)
		}
		metrics := append(slices.Clone(library.ComputedMetrics), library.StatDecisionsByOutcome, library.StatSearches, library.StatIndexers)
		figures := append(append(append(statFigures(rows), decided...), searched...), byIndexer...)
		if err := rollup.Replace(ctx, metrics, figures, now); err != nil {
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
