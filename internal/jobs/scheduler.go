package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// cronParser matches internal/platform/config's own validator and store.BackupSchedule's,
// so a schedule accepted anywhere in this codebase parses identically everywhere.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ErrAlreadyRunning is RunNow's refusal when a fire -- scheduled or manual --
// is already in flight for this task (ADR-59: "refused rather than queued
// twice").
var ErrAlreadyRunning = errors.New("jobs: this task is already running")

// ErrUnknownTask is a caller (the API, via a cmd/solarr adapter) asking
// about a task key TaskRegistry never registered.
var ErrUnknownTask = errors.New("jobs: unknown task")

// ErrNoFullMode is RunNowFull's refusal for a task that never registered a
// RunFull function.
var ErrNoFullMode = errors.New("jobs: this task has no full-run mode")

// ScheduleSource resolves a task's live, current cadence and enabled flag,
// re-read every tick so a setting change takes effect without a restart.
// Implemented by a cmd/solarr adapter over internal/db/settings.Cache --
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
		r.fireAndRecord(ctx, r.task.Enqueue)
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
	r.fireAndRecord(ctx, r.task.Enqueue)
	return nil
}

// RunNowFull is RunNow's counterpart for a task's own RunFull trigger
// (library scan's full rescan). Shares the same running lock as RunNow and
// the scheduled loop, so a full rescan and an ordinary one can never
// overlap for the same task.
func (r *TaskRunner) RunNowFull(ctx context.Context) error {
	if r.task.RunFull == nil {
		return ErrNoFullMode
	}
	if !r.running.TryLock() {
		return ErrAlreadyRunning
	}
	defer r.running.Unlock()
	r.fireAndRecord(ctx, r.task.RunFull)
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

func (r *TaskRunner) fireAndRecord(ctx context.Context, enqueue func(context.Context) (EnqueueResult, error)) {
	start := r.clock.Now()
	result, err := enqueue(ctx)
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
