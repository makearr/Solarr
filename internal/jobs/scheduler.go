package jobs

import (
	"context"
	"errors"
	"log/slog"
	"strings"
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

// ScheduleSource resolves a task's live cadence and enabled flag, re-read every tick so changes need no restart; a cmd/solarr adapter over settings.Cache implements it (architecture section 2).
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

// TaskRunner drives one Task's periodic loop (ADR-59): it resolves cadence and enabled each tick, fires, and records the outcome in jobs.db; the loop and RunNow share one mutex, so overlap is refused, not queued.
type TaskRunner struct {
	task    Task
	source  ScheduleSource
	states  *ScheduleStates
	clock   clock.Clock
	logger  *slog.Logger
	pauses  *Pauses
	running sync.Mutex

	// resolved remembers the last parse, so the cron strings are not parsed every minute and an invalid one is logged once.
	resolvedMu  sync.Mutex
	resolvedKey string
	resolved    cron.Schedule
}

// UsePauses makes the runner skip its fires, and refuse manual ones, while its task is paused.
func (r *TaskRunner) UsePauses(pauses *Pauses) { r.pauses = pauses }

// NewTaskRunner builds a runner for one task.
func NewTaskRunner(task Task, source ScheduleSource, states *ScheduleStates, cl clock.Clock, logger *slog.Logger) *TaskRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskRunner{task: task, source: source, states: states, clock: cl, logger: logger}
}

// catchUpMinInterval is the cadence gap above which a missed fire is made up; a task firing more often is close enough to its next fire.
const catchUpMinInterval = 15 * time.Minute

// catchUp fires once a run that fell due while the process was down. A task that fires at startup already did, so it is left alone.
func (r *TaskRunner) catchUp(ctx context.Context) {
	if !r.source.Enabled(r.task.Key) || r.source.RunAtStartup(r.task.Key, r.task.Startup == StartupOn) {
		return
	}
	state, found, err := r.states.Get(ctx, r.task.Key)
	now := r.clock.Now()
	if err != nil || !found || state.NextRunAt == nil || !state.NextRunAt.Before(now) {
		return
	}
	if state.LastRunAt != nil && !state.LastRunAt.Before(*state.NextRunAt) {
		return
	}
	if CadenceInterval(r.source.Cadences(r.task.Key, r.task.DefaultCadence), r.task.DefaultCadence, now) <= catchUpMinInterval {
		return
	}
	r.logger.Info("catching up a run missed while stopped", slog.String("task", r.task.Key), slog.Time("due", *state.NextRunAt))
	if err := r.runOnce(ctx, r.task.Enqueue); err != nil {
		r.logger.Debug("catch-up run skipped", slog.String("task", r.task.Key), slog.Any("error", err))
	}
}

// Run drives the loop until ctx ends.
func (r *TaskRunner) Run(ctx context.Context) {
	r.catchUp(ctx)
	for {
		schedule := r.resolveSchedule()
		next := schedule.Next(r.clock.Now())
		if err := r.states.SetNextRun(ctx, r.task.Key, next); err != nil {
			r.logger.Warn("recording next run failed", slog.String("task", r.task.Key), slog.Any("error", err))
		}

		if !r.sleepUntil(ctx, next) {
			return
		}
		if !r.source.Enabled(r.task.Key) {
			r.logger.Debug("schedule tick skipped: disabled", slog.String("task", r.task.Key))
			continue
		}

		if r.pauses.IsPaused(r.task.Key) {
			r.logger.Debug("schedule tick skipped: paused", slog.String("task", r.task.Key))
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

// scheduleRecheck bounds how long a cadence change waits to be seen: a weekly task changed to hourly must not sleep out the week.
const scheduleRecheck = time.Minute

// sleepUntil waits for next, re-resolving the cadence every scheduleRecheck and moving next when it changed; false once ctx ends.
func (r *TaskRunner) sleepUntil(ctx context.Context, next time.Time) bool {
	for {
		wait := min(r.clock.Until(next), scheduleRecheck)
		if err := r.clock.Sleep(ctx, wait); err != nil {
			return false
		}
		now := r.clock.Now()
		if !now.Before(next) {
			return true
		}
		if latest := r.resolveSchedule().Next(now); !latest.Equal(next) {
			next = latest
			if err := r.states.SetNextRun(ctx, r.task.Key, next); err != nil {
				r.logger.Warn("recording next run failed", slog.String("task", r.task.Key), slog.Any("error", err))
			}
		}
	}
}

// RunNow fires immediately even if the task is disabled (a manual override) and refuses when a fire is already in flight.
func (r *TaskRunner) RunNow(ctx context.Context) error {
	if r.task.EnqueueManual != nil {
		return r.runOnce(ctx, r.task.EnqueueManual)
	}
	return r.runOnce(ctx, r.task.Enqueue)
}

func (r *TaskRunner) runOnce(ctx context.Context, enqueue func(context.Context) (EnqueueResult, error)) error {
	if r.pauses.IsPaused(r.task.Key) {
		return ErrTaskPaused
	}
	if !r.running.TryLock() {
		return ErrAlreadyRunning
	}
	defer r.running.Unlock()
	r.fireAndRecord(ctx, enqueue)
	return nil
}

// RunNowFull is RunNow for a task's RunFull trigger (library scan's full rescan), sharing the running lock so full and ordinary runs never overlap.
func (r *TaskRunner) RunNowFull(ctx context.Context) error {
	if r.task.RunFull == nil {
		return ErrNoFullMode
	}
	if r.pauses.IsPaused(r.task.Key) {
		return ErrTaskPaused
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
	cadences := r.source.Cadences(r.task.Key, r.task.DefaultCadence)
	key := strings.Join(cadences, "\n")
	r.resolvedMu.Lock()
	defer r.resolvedMu.Unlock()
	if r.resolved != nil && r.resolvedKey == key {
		return r.resolved
	}
	var schedules earliest
	for _, cadence := range cadences {
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
		r.resolvedKey, r.resolved = key, fallback
		return fallback
	}
	r.resolvedKey, r.resolved = key, schedules
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
	result, err := enqueue(WithTaskKey(ctx, r.task.Key))
	duration := r.clock.Since(start)

	outcome, errMsg := "enqueued", ""
	switch {
	case err != nil:
		outcome, errMsg = "error", err.Error()
		r.logger.Warn("scheduled task failed", slog.String("task", r.task.Key), slog.Any("error", err))
	case result.Duplicate:
		outcome = "duplicate"
	}

	// A fire that happened records its outcome even if ctx is cancelled as Enqueue returns (shutdown racing the last tick), so a real run never leaves no trace.
	recordCtx := context.WithoutCancel(ctx)
	next := r.resolveSchedule().Next(r.clock.Now())
	if recordErr := r.states.RecordRun(recordCtx, r.task.Key, start, duration, outcome, errMsg, next); recordErr != nil {
		r.logger.Warn("recording schedule state failed", slog.String("task", r.task.Key), slog.Any("error", recordErr))
	}
}
