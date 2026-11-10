package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

const (
	// taskHoursFlushEvery is how often the hours of job run time are written to statistics.db.
	taskHoursFlushEvery = 30 * time.Second
	// taskHoursKeepDays is how many days of hours are kept; the heatmap shows seven.
	taskHoursKeepDays = 8
)

// startTaskHistory writes the scheduler's run-time hours to statistics.db as they accumulate, so the heatmap survives
// a restart, and drops those older than the heatmap looks.
func (p *process) startTaskHistory(ctx context.Context) {
	go func() {
		wall := clock.NewSystem()
		ticker := wall.NewTicker(taskHoursFlushEvery)
		defer ticker.Stop()
		var pruned time.Time
		for {
			select {
			case <-ctx.Done():
				flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_ = p.taskHours.Flush(flushCtx)
				cancel()
				return
			case <-ticker.C():
				if err := p.taskHours.Flush(ctx); err != nil {
					p.logger.Warn("write the scheduler's run times", slog.Any("error", err))
				}
				if now := wall.Now(); now.Sub(pruned) >= time.Hour {
					if err := p.taskHours.Prune(ctx, now.Add(-taskHoursKeepDays*24*time.Hour)); err != nil {
						p.logger.Warn("drop old run times", slog.Any("error", err))
					}
					pruned = now
				}
			}
		}
	}()
}
