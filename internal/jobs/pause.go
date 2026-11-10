package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/jobsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// AllTasks is the pause scope that holds every task.
const AllTasks = "*"

// ErrPaused is the cancellation cause of a running job a pause interrupted; the executor puts it back in the queue.
var ErrPaused = errors.New("jobs: paused")

// ErrTaskPaused is RunNow's refusal for a paused task: its jobs would only wait.
var ErrTaskPaused = errors.New("jobs: this task is paused")

type taskKeyContext struct{}

// WithTaskKey marks every job enqueued with ctx as started by the task, so a pause can find it.
func WithTaskKey(ctx context.Context, taskKey string) context.Context {
	return context.WithValue(ctx, taskKeyContext{}, taskKey)
}

// TaskKeyFrom is the task ctx was marked with, empty for work no task started.
func TaskKeyFrom(ctx context.Context) string {
	key, _ := ctx.Value(taskKeyContext{}).(string)
	return key
}

type activeJob struct {
	taskKey string
	cancel  context.CancelCauseFunc
}

// Pauses holds tasks back and interrupts their running jobs; the pauses live in jobs.db, so a restart keeps them and
// the interrupted jobs wait in the queue where they were put back.
type Pauses struct {
	store   *store.Store
	queries *jobsdb.Queries
	clock   clock.Clock

	mu     sync.Mutex
	paused map[string]time.Time
	active map[int64]activeJob
}

// NewPauses builds the pauses over jobStore; call Load before use.
func NewPauses(jobStore *store.Store, cl clock.Clock) *Pauses {
	return &Pauses{
		store: jobStore, queries: jobsdb.New(jobStore.Read()), clock: cl,
		paused: map[string]time.Time{}, active: map[int64]activeJob{},
	}
}

// Load reads the stored pauses; once at start-up.
func (p *Pauses) Load(ctx context.Context) error {
	rows, err := p.queries.ListTaskPauses(ctx)
	if err != nil {
		return fmt.Errorf("jobs: load pauses: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, row := range rows {
		p.paused[row.Scope] = time.Unix(row.PausedAt, 0).UTC()
	}
	return nil
}

// Pause holds scope (a task key or AllTasks) and interrupts its running jobs, which go back to the queue unfinished.
func (p *Pauses) Pause(ctx context.Context, scope string) error {
	now := p.clock.Now()
	err := p.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		return jobsdb.New(tx).PauseTask(ctx, jobsdb.PauseTaskParams{Scope: scope, PausedAt: now.Unix()})
	})
	if err != nil {
		return fmt.Errorf("jobs: pause %s: %w", scope, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, already := p.paused[scope]; !already {
		p.paused[scope] = time.Unix(now.Unix(), 0).UTC()
	}
	for _, running := range p.active {
		if running.taskKey != "" && (scope == AllTasks || scope == running.taskKey) {
			running.cancel(ErrPaused)
		}
	}
	return nil
}

// Resume lets scope's jobs be claimed again; they continue where they were put back.
func (p *Pauses) Resume(ctx context.Context, scope string) error {
	err := p.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		return jobsdb.New(tx).ResumeTask(ctx, scope)
	})
	if err != nil {
		return fmt.Errorf("jobs: resume %s: %w", scope, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.paused, scope)
	return nil
}

// IsPaused reports whether work of taskKey is held, by its own pause or by AllTasks; nil-safe.
func (p *Pauses) IsPaused(taskKey string) bool {
	if p == nil || taskKey == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, own := p.paused[taskKey]
	_, all := p.paused[AllTasks]
	return own || all
}

// PausedAt is when scope itself was paused.
func (p *Pauses) PausedAt(scope string) (time.Time, bool) {
	if p == nil {
		return time.Time{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	at, found := p.paused[scope]
	return at, found
}

// Held counts, per task key, the jobs waiting in the queue; for the ones paused these are the held and interrupted.
func (p *Pauses) Held(ctx context.Context) (map[string]int64, error) {
	rows, err := p.queries.CountHeldJobsByTask(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs: count held jobs: %w", err)
	}
	held := make(map[string]int64, len(rows))
	for _, row := range rows {
		held[row.TaskKey] = row.Held
	}
	return held, nil
}

// taskOf is the task that started a job, empty when none did.
func (p *Pauses) taskOf(ctx context.Context, jobID int64) string {
	if p == nil {
		return ""
	}
	key, err := p.queries.GetJobTask(ctx, jobID)
	if err != nil {
		return ""
	}
	return key
}

// track lists a running job so a pause can cancel it; call the returned func when it ends.
func (p *Pauses) track(jobID int64, taskKey string, cancel context.CancelCauseFunc) func() {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	p.active[jobID] = activeJob{taskKey: taskKey, cancel: cancel}
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.active, jobID)
	}
}
