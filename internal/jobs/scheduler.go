package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/robfig/cron/v3"

	"github.com/makearr/MediaNexus/internal/clock"
)

// cronParser matches internal/config's own validator and store.BackupSchedule's,
// so a schedule accepted anywhere in this codebase parses identically everywhere.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Scheduler periodically calls Fire until ctx ends, on a cron expression and
// the injected clock (ADR-17) -- the same shape store.BackupSchedule already
// uses for its own periodic backups, generalized here to any periodic action
// rather than one tied to a specific store. Scan, enrichment, probing,
// capacity polling and subscription expansion were all "enqueued once at
// startup, waits for a restart" before this existed; Fire is one of their own
// enqueue functions, called again on every tick rather than only once.
type Scheduler struct {
	name     string
	schedule cron.Schedule
	clock    clock.Clock
	logger   *slog.Logger
	fire     func(context.Context)
}

// NewScheduler parses expr -- a five-field cron expression -- and builds a
// scheduler that calls fire on every tick. name is for logging only.
func NewScheduler(
	name, expr string, cl clock.Clock, logger *slog.Logger, fire func(context.Context),
) (*Scheduler, error) {
	parsed, err := cronParser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("jobs: schedule %q for %s: %w", expr, name, err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{name: name, schedule: parsed, clock: cl, logger: logger, fire: fire}, nil
}

// Run drives the loop until ctx ends. fire panicking is not guarded against
// here -- each of today's callers is already itself a top-level enqueue
// function that only logs its own errors, never panics on one job failing to
// enqueue (the same per-item isolation EnrichJob's own Run uses).
func (s *Scheduler) Run(ctx context.Context) {
	for {
		next := s.schedule.Next(s.clock.Now())
		if err := s.clock.Sleep(ctx, s.clock.Until(next)); err != nil {
			return
		}
		s.logger.Debug("scheduled fire", slog.String("schedule", s.name))
		s.fire(ctx)
	}
}
