package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
)

// Mode is the dry-run level (ADR-20). It is declared here rather than taken
// from internal/config because the executor's concern is only whether mutation
// is permitted; the config package's Mode is the same three names parsed from
// config.yml, and main maps one onto the other.
type Mode string

// The dry-run modes.
const (
	// ModeNormal acts on decisions.
	ModeNormal Mode = "normal"
	// ModeObserve scans and reports what it would do, contacting nothing.
	ModeObserve Mode = "observe"
	// ModeShadow adds indexer polling and full scoring, still writing nothing.
	ModeShadow Mode = "shadow"
)

// AllowsMutation reports whether jobs that change something may run.
func (m Mode) AllowsMutation() bool { return m == ModeNormal || m == "" }

// Handler runs one job. Returning a classified *Error decides what happens
// next; anything else is classified by Classify.
type Handler interface {
	Run(ctx context.Context, job Job) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, job Job) error

// Run satisfies Handler.
func (f HandlerFunc) Run(ctx context.Context, job Job) error { return f(ctx, job) }

// ErrNoHandler reports a job whose type has no handler registered. It is a
// wiring mistake, so it fails fatally rather than retrying five times.
var ErrNoHandler = errors.New("jobs: no handler registered")

// Executor runs claimed jobs. It is the single choke point for the dry-run gate
// (ADR-20, Invariant 31): mutation is refused here and checked nowhere else,
// because a flag consulted throughout the codebase guarantees a missed check.
type Executor struct {
	queue    *Queue
	clock    clock.Clock
	logger   *slog.Logger
	owner    string
	mode     Mode
	leaseTTL time.Duration

	mu       sync.RWMutex
	handlers map[Type]Handler
}

// ExecutorConfig wires an executor.
type ExecutorConfig struct {
	// Owner identifies this process in a lease. A restart must not reuse the
	// previous value, or the reaper cannot tell a stale lease from a live one.
	Owner    string
	Mode     Mode
	LeaseTTL time.Duration
	Logger   *slog.Logger
}

// NewExecutor builds an executor.
func NewExecutor(queue *Queue, cl clock.Clock, config ExecutorConfig) *Executor {
	if config.LeaseTTL <= 0 {
		config.LeaseTTL = DefaultLeaseTTL
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Mode == "" {
		config.Mode = ModeNormal
	}
	return &Executor{
		queue:    queue,
		clock:    cl,
		logger:   config.Logger.With(slog.String("owner", config.Owner)),
		owner:    config.Owner,
		mode:     config.Mode,
		leaseTTL: config.LeaseTTL,
		handlers: make(map[Type]Handler),
	}
}

// Handle registers the handler for a job type.
func (e *Executor) Handle(jobType Type, handler Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[jobType] = handler
}

// Owner reports this executor's lease identity.
func (e *Executor) Owner() string { return e.owner }

// LeaseTTL reports how long this executor's leases last. The dispatcher claims
// with the same value: two places configuring one TTL means a claim can outlive
// its renewals, or a renewal can shorten a lease, depending which is larger.
func (e *Executor) LeaseTTL() time.Duration { return e.leaseTTL }

// Execute runs one claimed job to completion and records the outcome. It never
// returns the handler's error: the outcome is recorded in the queue, which is
// where anything interested reads it from.
func (e *Executor) Execute(ctx context.Context, job Job) {
	logger := e.logger.With(slog.Int64("job_id", job.ID), slog.String("type", string(job.Type)))

	// The gate, before anything is started or an attempt recorded: in a dry-run
	// mode a mutating job must leave no trace of having run.
	if job.Mutating && !e.mode.AllowsMutation() {
		e.refuse(ctx, job, logger)
		return
	}

	handler, found := e.handler(job.Type)
	if !found {
		e.failWithoutHandler(ctx, job, logger)
		return
	}

	attempt, err := e.queue.Start(ctx, job, e.owner)
	if err != nil {
		// Losing the lease between claim and start is normal under a slow
		// process: the reaper took the job back and someone else will run it.
		logger.Warn("could not start job", slog.Any("error", err))
		return
	}

	running, stopRenewing := context.WithCancel(ctx)
	renewalDone := e.renewUntilDone(running, stopRenewing, job, logger)

	runErr := runWithRecovery(running, handler, job)

	stopRenewing()
	<-renewalDone

	// Completion and failure are recorded against the parent context: the
	// running context has just been cancelled, and a finished job must be
	// recorded even so.
	if runErr == nil {
		if err := e.queue.Complete(ctx, job, e.owner, attempt); err != nil {
			logger.Warn("could not record success", slog.Any("error", err))
		}
		return
	}

	resolution, err := e.queue.Fail(ctx, job, e.owner, attempt, runErr)
	if err != nil {
		logger.Warn("could not record failure", slog.Any("error", err))
		return
	}
	logger.Log(ctx, levelFor(resolution.Class), "job failed",
		slog.String("class", string(resolution.Class)),
		slog.Bool("retrying", resolution.Retrying),
		slog.Any("error", runErr))
}

// refuse cancels a mutating job in a dry-run mode.
//
// The planner should not enqueue mutating work in observe or shadow mode at
// all; this is the backstop that makes ADR-20 a guarantee rather than a
// convention, so it records why rather than failing silently.
func (e *Executor) refuse(ctx context.Context, job Job, logger *slog.Logger) {
	logger.Info("refusing a mutating job", slog.String("mode", string(e.mode)))
	if err := e.queue.CancelWithReason(ctx, job.ID,
		fmt.Sprintf("refused: %s is mutating and the mode is %s", job.Type, e.mode)); err != nil {
		logger.Warn("could not record the refusal", slog.Any("error", err))
	}
}

func (e *Executor) failWithoutHandler(ctx context.Context, job Job, logger *slog.Logger) {
	cause := Fatalf("jobs.execute", string(job.Type), ErrNoHandler)
	attempt, err := e.queue.Start(ctx, job, e.owner)
	if err != nil {
		logger.Warn("could not start job", slog.Any("error", err))
		return
	}
	if _, err := e.queue.Fail(ctx, job, e.owner, attempt, cause); err != nil {
		logger.Warn("could not record failure", slog.Any("error", err))
	}
	logger.Error("no handler for job type", slog.Any("error", cause))
}

func (e *Executor) handler(jobType Type) (Handler, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	handler, found := e.handlers[jobType]
	return handler, found
}

// renewUntilDone keeps the lease alive while the handler runs, and cancels the
// handler if the lease is lost. A worker whose lease was reaped must stop
// rather than keep working on a job that now belongs to someone else.
func (e *Executor) renewUntilDone(ctx context.Context, cancel context.CancelFunc,
	job Job, logger *slog.Logger) <-chan struct{} {
	done := make(chan struct{})

	// A third of the TTL, so two consecutive renewals can be missed before the
	// lease actually expires.
	interval := e.leaseTTL / 3
	if interval <= 0 {
		close(done)
		return done
	}

	go func() {
		defer close(done)
		ticker := e.clock.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				if err := e.queue.Renew(ctx, job, e.owner, e.leaseTTL); err != nil {
					if errors.Is(err, ErrLeaseLost) {
						logger.Warn("lease lost; stopping work on this job")
						cancel()
						return
					}
					logger.Warn("could not renew lease", slog.Any("error", err))
				}
			}
		}
	}()
	return done
}

// runWithRecovery turns a panicking handler into a failed job rather than a
// dead process. A panic is a programming error, so it is fatal: retrying could
// only reproduce it (Invariant 26 for plugins, and the same reasoning here).
func runWithRecovery(ctx context.Context, handler Handler, job Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = Fatalf("jobs.execute", string(job.Type),
				fmt.Errorf("panic: %v\n%s", recovered, debug.Stack()))
		}
	}()
	return handler.Run(ctx, job)
}

func levelFor(class Class) slog.Level {
	switch class {
	case ClassFatal:
		return slog.LevelError
	case ClassTerminal:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
