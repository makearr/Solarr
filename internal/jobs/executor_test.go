package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func newExecutor(t *testing.T, queue *jobs.Queue, cl *clock.Fake, mode jobs.Mode) *jobs.Executor {
	t.Helper()
	return jobs.NewExecutor(queue, cl, jobs.ExecutorConfig{
		Owner: "worker-1", Mode: mode, LeaseTTL: time.Minute, Logger: quietLogger(),
	})
}

func TestExecute_RunsAHandlerAndRecordsSuccess(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	var ran bool
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		ran = true
		return nil
	}))

	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	if !ran {
		t.Fatal("the handler did not run")
	}
	finished, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if finished.State != jobs.StateSucceeded {
		t.Errorf("State = %q, want SUCCEEDED", finished.State)
	}
}

// A long sweep yields its one-slot pool: it succeeds, and its continuation runs after the job
// that was already waiting rather than ahead of it.
func TestExecute_AYieldedJobContinuesBehindTheWaitingOnes(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		return fmt.Errorf("probe: 2 minutes spent: %w", jobs.ErrYield)
	}))

	sweep := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, ActionKey: "sweep", Payload: []byte(`{"root":1}`)})
	fake.Advance(time.Second)
	waiting := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, ActionKey: "waiting"})
	fake.Advance(time.Second)
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	finished, err := queue.Get(t.Context(), sweep.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if finished.State != jobs.StateSucceeded {
		t.Errorf("yielded job state = %q, want SUCCEEDED", finished.State)
	}
	next := claimOne(t, queue, jobs.PoolMaintenance, "worker-1")
	if next.ID != waiting.ID {
		t.Fatalf("claimed job %d next, want the waiting job %d", next.ID, waiting.ID)
	}
	continuation := claimOne(t, queue, jobs.PoolMaintenance, "worker-1")
	if continuation.ActionKey != "sweep" || string(continuation.Payload) != `{"root":1}` || continuation.ID == sweep.ID {
		t.Errorf("continuation = %+v, want a new job carrying the sweep's key and payload", continuation)
	}
}

func TestExecute_RecordsAFailure(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		return jobs.Terminalf("import.run", "", errors.New("no media files"))
	}))

	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeImport})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolImport, "worker-1"))

	failed, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if failed.State != jobs.StateFailed {
		t.Errorf("State = %q, want FAILED", failed.State)
	}
	if failed.LastError == "" {
		t.Error("LastError is empty")
	}
}

// A panic is a programming error, so it fails the job rather than the process,
// and it is fatal: retrying could only reproduce it.
func TestExecute_RecoversFromAPanic(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		var nothing *struct{ field int }
		return errors.New(string(rune(nothing.field))) // nil dereference
	}))

	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeImport})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolImport, "worker-1"))

	failed, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if failed.State != jobs.StateFailed {
		t.Errorf("State = %q after a panic, want FAILED", failed.State)
	}
	if failed.Attempts != 1 {
		t.Errorf("Attempts = %d; a panic is fatal and must not consume retries", failed.Attempts)
	}
}

// ADR-20: the executor refuses mutating job types, and it is checked here and nowhere else,
// because a flag consulted throughout the codebase guarantees a missed check (Invariant 31).
func TestExecute_RefusesAMutatingJobInObserveMode(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeObserve)

	var ran bool
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		ran = true
		return nil
	}))

	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeImport}) // mutating
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolImport, "worker-1"))

	if ran {
		t.Fatal("a mutating handler ran in observe mode")
	}
	refused, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if refused.State != jobs.StateCancelled {
		t.Errorf("State = %q, want CANCELLED", refused.State)
	}
	if refused.LastError == "" {
		t.Error("the refusal was not recorded; a job that vanished must explain itself")
	}
	if refused.Attempts != 0 {
		t.Errorf("Attempts = %d; a refused job must leave no trace of having run", refused.Attempts)
	}
}

func TestExecute_RunsNonMutatingJobsInObserveMode(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeObserve)

	var ran bool
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		ran = true
		return nil
	}))

	// Observe mode still scans and reports; only mutation is refused.
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	if !ran {
		t.Error("a non-mutating job was refused in observe mode")
	}
}

func TestExecute_ShadowModeAlsoRefusesMutation(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeShadow)
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		t.Error("a mutating handler ran in shadow mode")
		return nil
	}))

	mustEnqueue(t, queue, jobs.Request{Type: typeImport})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolImport, "worker-1"))
}

// A wiring mistake, so it fails fatally rather than retrying five times.
func TestExecute_MissingHandlerFailsFatally(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	failed, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if failed.State != jobs.StateFailed {
		t.Errorf("State = %q, want FAILED", failed.State)
	}
	if failed.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 rather than the full retry budget", failed.Attempts)
	}
}

// A worker whose lease was reaped must stop, rather than keep working on a job
// that now belongs to someone else.
func TestExecute_StopsWhenTheLeaseIsLost(t *testing.T) {
	queue, fake := newQueue(t)
	executor := jobs.NewExecutor(queue, fake, jobs.ExecutorConfig{
		Owner: "slow-worker", Mode: jobs.ModeNormal, LeaseTTL: 30 * time.Second, Logger: quietLogger(),
	})

	started := make(chan struct{})
	stopped := make(chan struct{})
	executor.Handle(typeBackup, jobs.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		close(started)
		<-ctx.Done() // the renewal loop cancels this when the lease is lost
		close(stopped)
		return ctx.Err()
	}))

	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	claimed := claimOneWithTTL(t, queue, jobs.PoolMaintenance, "slow-worker", 30*time.Second)

	go executor.Execute(context.Background(), claimed)
	<-started

	// Expire the lease, reap it, and tick renewals until one discovers the loss; the
	// renewal timer may or may not be registered yet, so no single Advance is awaited.
	deadline := time.After(5 * time.Second)
	for {
		fake.Advance(time.Minute)
		if _, err := queue.ReclaimExpired(context.Background()); err != nil {
			t.Fatalf("ReclaimExpired: %v", err)
		}
		select {
		case <-stopped:
			return
		case <-deadline:
			t.Fatal("the handler was not cancelled after its lease was reclaimed")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestExecute_RenewsTheLeaseWhileRunning(t *testing.T) {
	queue, fake := newQueue(t)
	executor := jobs.NewExecutor(queue, fake, jobs.ExecutorConfig{
		Owner: "worker-1", Mode: jobs.ModeNormal, LeaseTTL: 30 * time.Second, Logger: quietLogger(),
	})

	release := make(chan struct{})
	started := make(chan struct{})
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		close(started)
		<-release
		return nil
	}))

	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	// The same TTL the executor renews with, as the dispatcher uses in
	// production: claiming with a longer one would make renewal shorten it.
	claimed := claimOneWithTTL(t, queue, jobs.PoolMaintenance, "worker-1", 30*time.Second)

	done := make(chan struct{})
	go func() {
		executor.Execute(context.Background(), claimed)
		close(done)
	}()
	<-started

	before, err := queue.Get(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	fake.BlockUntilWaiters(1)
	fake.Advance(10 * time.Second) // one renewal interval

	// Renewal has to actually extend the lease, or a long job is reaped out
	// from under itself.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		after, err := queue.Get(context.Background(), enqueued.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if after.LeaseExpiresAt.After(before.LeaseExpiresAt) {
			close(release)
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	<-done
	t.Error("the lease was never extended while the job ran")
}
