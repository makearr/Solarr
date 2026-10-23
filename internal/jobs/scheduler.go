package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// cronParser matches internal/platform/config's own validator,
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
	// Cadences returns the task's effective cron expressions: an override if one is stored,
	// defaultCadence otherwise. The task fires at the earliest of them.
	Cadences(taskKey, defaultCadence string) []string
	// RunAtStartup reports whether the task fires once at startup; fallback is its default.
	RunAtStartup(taskKey string, fallback bool) bool
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
	if r.task.EnqueueManual != nil {
		return r.runOnce(ctx, r.task.EnqueueManual)
	}
	return r.runOnce(ctx, r.task.Enqueue)
}

func (r *TaskRunner) runOnce(ctx context.Context, enqueue func(context.Context) (EnqueueResult, error)) error {
	if !r.running.TryLock() {
		return ErrAlreadyRunning
	}
	defer r.running.Unlock()
	r.fireAndRecord(ctx, enqueue)
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

// Startup fires the task once at process start when it is enabled and set to. A heavy task (StartupDelay) waits
// first and is skipped when it last ran within its own shortest interval; call it on its own goroutine then.
func (r *TaskRunner) Startup(ctx context.Context) {
	if !r.source.Enabled(r.task.Key) || !r.source.RunAtStartup(r.task.Key, r.task.Startup == StartupOn) {
		return
	}
	if r.task.StartupDelay > 0 {
		if r.ranWithinItsInterval(ctx) {
			r.logger.Info("startup run skipped: the task ran within its own interval", slog.String("task", r.task.Key))
			return
		}
		if err := r.clock.Sleep(ctx, r.task.StartupDelay); err != nil {
			return
		}
	}
	if err := r.runOnce(ctx, r.task.Enqueue); err != nil {
		r.logger.Debug("startup run skipped", slog.String("task", r.task.Key), slog.Any("error", err))
	}
}

// ranWithinItsInterval reports whether the last run is younger than the gap between the schedule's next two firings.
func (r *TaskRunner) ranWithinItsInterval(ctx context.Context) bool {
	state, found, err := r.states.Get(ctx, r.task.Key)
	if err != nil || !found || state.LastRunAt == nil {
		return false
	}
	now := r.clock.Now()
	schedule := r.resolveSchedule()
	first := schedule.Next(now)
	second := schedule.Next(first)
	if first.IsZero() || second.IsZero() {
		return false
	}
	return now.Sub(*state.LastRunAt) < second.Sub(first)
}

// resolveSchedule parses the task's live cadences, dropping any now unparseable -- write-time
// validation should have caught it, but a scheduler must never wedge on a bad value -- and falls
// back to the already-validated DefaultCadence when none is left.
func (r *TaskRunner) resolveSchedule() cron.Schedule {
	var schedules earliest
	for _, cadence := range r.source.Cadences(r.task.Key, r.task.DefaultCadence) {
		parsed, err := cronParser.Parse(cadence)
		if err != nil {
			r.logger.Error("schedule override is invalid, ignoring it",
				slog.String("task", r.task.Key), slog.String("cadence", cadence), slog.Any("error", err))
			continue
		}
		schedules = append(schedules, parsed)
	}
	if len(schedules) == 0 {
		fallback, _ := cronParser.Parse(r.task.DefaultCadence)
		return fallback
	}
	return schedules
}

// CadenceInterval is the gap between the next two firings of the earliest of cadences after now, falling back to
// fallback when none parses; zero when neither can fire.
func CadenceInterval(cadences []string, fallback string, now time.Time) time.Duration {
	var schedules earliest
	for _, cadence := range cadences {
		if parsed, err := cronParser.Parse(cadence); err == nil {
			schedules = append(schedules, parsed)
		}
	}
	if len(schedules) == 0 {
		parsed, err := cronParser.Parse(fallback)
		if err != nil {
			return 0
		}
		schedules = earliest{parsed}
	}
	first := schedules.Next(now)
	second := schedules.Next(first)
	if first.IsZero() || second.IsZero() {
		return 0
	}
	return second.Sub(first)
}

// earliest fires at the soonest of several schedules.
type earliest []cron.Schedule

func (e earliest) Next(after time.Time) time.Time {
	var next time.Time
	for _, schedule := range e {
		candidate := schedule.Next(after)
		if candidate.IsZero() {
			continue
		}
		if next.IsZero() || candidate.Before(next) {
			next = candidate
		}
	}
	return next
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
