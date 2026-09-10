package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// cronParser matches internal/platform/config's own validator and store.BackupSchedule's,
// so a schedule accepted anywhere in this codebase parses identically everywhere.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Scheduler periodically calls Fire until ctx ends, on a fixed cron
// expression and the injected clock (ADR-17) -- the same shape
// store.BackupSchedule already uses for its own periodic backups. Unlike
// TaskRunner below, its cadence never changes after construction; it is
// what store.BackupSchedule's own rotation still uses, not ADR-59's
// registered tasks.
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

// ErrAlreadyRunning is RunNow's refusal when a fire -- scheduled or manual --
// is already in flight for this task (ADR-59: "refused rather than queued
// twice").
var ErrAlreadyRunning = errors.New("jobs: this task is already running")

// ErrUnknownTask is a caller (the API, via a cmd/medianexus adapter) asking
// about a task key TaskRegistry never registered.
var ErrUnknownTask = errors.New("jobs: unknown task")

// ScheduleSource resolves a task's live, current cadence and enabled flag,
// re-read every tick so a setting change takes effect without a restart.
// Implemented by a cmd/medianexus adapter over internal/db/settings.Cache --
// this package never imports internal/db/settings (architecture §2).
type ScheduleSource interface {
	// Cadence returns the task's effective cron expression: an override if
	// one is stored, defaultCadence otherwise.
	Cadence(taskKey, defaultCadence string) string
	// Enabled reports whether the task should fire at all. A disabled task
	// still has its next-run time recomputed and shown -- it is skipped,
	// not un-scheduled.
	Enabled(taskKey string) bool
}

// TaskRunner drives one registered Task's own periodic loop (ADR-59):
// resolves its live cadence and enabled flag every tick, fires, and records
// what happened in jobs.db so a restart does not erase the only evidence a
// task has not run in a week. The scheduled loop and a manual RunNow share
// one mutex, so "already running" is refused rather than double-queued in
// either direction.
type TaskRunner struct {
	task    Task
	source  ScheduleSource
	states  *ScheduleStates
	clock   clock.Clock
	logger  *slog.Logger
	running sync.Mutex
}

// NewTaskRunner builds a runner for one task.
func NewTaskRunner(task Task, source ScheduleSource, states *ScheduleStates, cl clock.Clock, logger *slog.Logger) *TaskRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskRunner{task: task, source: source, states: states, clock: cl, logger: logger}
}

// Run drives the loop until ctx ends.
func (r *TaskRunner) Run(ctx context.Context) {
	for {
		schedule := r.resolveSchedule()
		next := schedule.Next(r.clock.Now())
		if err := r.states.SetNextRun(ctx, r.task.Key, next); err != nil {
			r.logger.Warn("recording next run failed", slog.String("task", r.task.Key), slog.Any("error", err))
		}

		if err := r.clock.Sleep(ctx, r.clock.Until(next)); err != nil {
			return
		}
		if !r.source.Enabled(r.task.Key) {
			r.logger.Debug("schedule tick skipped: disabled", slog.String("task", r.task.Key))
			continue
		}

		if !r.running.TryLock() {
			r.logger.Debug("schedule tick skipped: already running", slog.String("task", r.task.Key))
			continue
		}
		r.fireAndRecord(ctx)
		r.running.Unlock()
	}
}

// RunNow fires immediately, ignoring whether the task is enabled -- a
// manual trigger is the user overriding that, the same way a disabled
// scheduled scan can still be run from the interface once. Refuses rather
// than double-queues when a fire is already in flight.
func (r *TaskRunner) RunNow(ctx context.Context) error {
	if !r.running.TryLock() {
		return ErrAlreadyRunning
	}
	defer r.running.Unlock()
	r.fireAndRecord(ctx)
	return nil
}

// resolveSchedule parses the task's live cadence, falling back to its
// already-validated DefaultCadence if an override is now unparseable --
// write-time validation (SetScheduleCadence) is what should have caught
// this, but a scheduler must never wedge on a bad value it did not itself
// just accept.
func (r *TaskRunner) resolveSchedule() cron.Schedule {
	cadence := r.source.Cadence(r.task.Key, r.task.DefaultCadence)
	parsed, err := cronParser.Parse(cadence)
	if err == nil {
		return parsed
	}
	r.logger.Error("schedule override is invalid, falling back to the default",
		slog.String("task", r.task.Key), slog.String("cadence", cadence), slog.Any("error", err))
	// DefaultCadence was already validated at TaskRegistry.Register, so this
	// cannot itself fail.
	fallback, _ := cronParser.Parse(r.task.DefaultCadence)
	return fallback
}

func (r *TaskRunner) fireAndRecord(ctx context.Context) {
	start := r.clock.Now()
	result, err := r.task.Enqueue(ctx)
	duration := r.clock.Since(start)

	outcome, errMsg := "enqueued", ""
	switch {
	case err != nil:
		outcome, errMsg = "error", err.Error()
		r.logger.Warn("scheduled task failed", slog.String("task", r.task.Key), slog.Any("error", err))
	case result.Duplicate:
		outcome = "duplicate"
	}

	// A fire that already happened records its own outcome even if ctx is
	// cancelled the instant Enqueue returns (process shutdown racing the
	// last tick) -- the alternative is a fire that genuinely ran leaving no
	// trace, which is exactly what this table exists to prevent.
	recordCtx := context.WithoutCancel(ctx)
	next := r.resolveSchedule().Next(r.clock.Now())
	if recordErr := r.states.RecordRun(recordCtx, r.task.Key, start, duration, outcome, errMsg, next); recordErr != nil {
		r.logger.Warn("recording schedule state failed", slog.String("task", r.task.Key), slog.Any("error", recordErr))
	}
}
