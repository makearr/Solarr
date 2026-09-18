package jobs_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/jobs"
)

func TestDispatcher_RunsQueuedWork(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	var completed atomic.Int64
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		completed.Add(1)
		return nil
	}))
	dispatcher := jobs.NewDispatcher(queue, fake, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{{Pool: jobs.PoolMaintenance, Capacity: 4}},
		Tick:  time.Second, Logger: quietLogger(),
	})

	for range 5 {
		mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	}

	// Two ticks: four fit the pool, the fifth follows.
	dispatcher.Tick(t.Context())
	waitForQuiet(t, dispatcher)
	dispatcher.Tick(t.Context())
	waitForQuiet(t, dispatcher)

	if got := completed.Load(); got != 5 {
		t.Errorf("completed %d jobs, want 5", got)
	}
}

// Invariant 23: the scheduler enqueues, it never executes. A blocked job must
// not stall the loop, or one slow import stops every other pool.
func TestDispatcher_TickDoesNotWaitForAJob(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	var once sync.Once
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}))

	dispatcher := jobs.NewDispatcher(queue, fake, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{{Pool: jobs.PoolImport, Capacity: 1}},
		Tick:  time.Second, Logger: quietLogger(),
	})
	mustEnqueue(t, queue, jobs.Request{Type: typeImport})

	returned := make(chan struct{})
	go func() {
		dispatcher.Tick(t.Context())
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Tick blocked on a running job")
	}
	<-started
}

// Import concurrency is fixed at 1 and is not configurable (ADR-3).
func TestDispatcher_ImportPoolIsAlwaysOne(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	var concurrent, peak atomic.Int64
	release := make(chan struct{})
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		current := concurrent.Add(1)
		for {
			was := peak.Load()
			if current <= was || peak.CompareAndSwap(was, current) {
				break
			}
		}
		<-release
		concurrent.Add(-1)
		return nil
	}))

	// Ask for eight; ADR-3 says one, whatever the configuration says.
	dispatcher := jobs.NewDispatcher(queue, fake, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{{Pool: jobs.PoolImport, Capacity: 8}},
		Tick:  time.Second, Logger: quietLogger(),
	})
	for range 4 {
		mustEnqueue(t, queue, jobs.Request{Type: typeImport})
	}

	dispatcher.Tick(t.Context())
	time.Sleep(50 * time.Millisecond)
	if got := dispatcher.InFlight()[jobs.PoolImport]; got != 1 {
		t.Errorf("in flight = %d, want 1", got)
	}
	close(release)
	waitForQuiet(t, dispatcher)

	if got := peak.Load(); got != 1 {
		t.Errorf("peak import concurrency = %d, want 1 (ADR-3)", got)
	}
}

func TestDispatcher_PoolsAreIndependent(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	blocked := make(chan struct{})
	defer close(blocked)
	var backupsRun atomic.Int64
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		<-blocked
		return nil
	}))
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		backupsRun.Add(1)
		return nil
	}))

	dispatcher := jobs.NewDispatcher(queue, fake, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{
			{Pool: jobs.PoolImport, Capacity: 1},
			{Pool: jobs.PoolMaintenance, Capacity: 2},
		},
		Tick: time.Second, Logger: quietLogger(),
	})

	mustEnqueue(t, queue, jobs.Request{Type: typeImport})
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	dispatcher.Tick(t.Context())

	// The maintenance job must finish while the import is still blocked.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if backupsRun.Load() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("a blocked import pool stopped the maintenance pool")
}

func TestDispatcher_DoesNotPollAnUnsizedPool(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)
	executor.Handle(typeImport, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		t.Error("a job ran in a pool that was never sized")
		return nil
	}))

	// A deployment must not accidentally run work it did not size.
	dispatcher := jobs.NewDispatcher(queue, fake, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{{Pool: jobs.PoolMaintenance, Capacity: 1}},
		Tick:  time.Second, Logger: quietLogger(),
	})
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeImport})

	dispatcher.Tick(t.Context())
	time.Sleep(50 * time.Millisecond)

	untouched, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if untouched.State != jobs.StatePending {
		t.Errorf("State = %q, want it left PENDING", untouched.State)
	}
}

func TestDispatcher_ReclaimsAbandonedWorkOnTick(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	var ran atomic.Int64
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		ran.Add(1)
		return nil
	}))
	dispatcher := jobs.NewDispatcher(queue, fake, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{{Pool: jobs.PoolMaintenance, Capacity: 1}},
		Tick:  time.Second, Logger: quietLogger(),
	})

	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	// A previous process claimed it and died.
	crashed := claimOne(t, queue, jobs.PoolMaintenance, "crashed-process")
	if _, err := queue.Start(t.Context(), crashed, "crashed-process"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	fake.Advance(2 * time.Minute)

	// One tick reclaims and re-dispatches: restart recovery with no manual
	// cleanup.
	dispatcher.Tick(t.Context())
	waitForQuiet(t, dispatcher)

	if got := ran.Load(); got != 1 {
		t.Fatalf("the abandoned job ran %d times, want 1", got)
	}
	finished, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if finished.State != jobs.StateSucceeded {
		t.Errorf("State = %q, want SUCCEEDED", finished.State)
	}
}

func TestDispatcher_RunStopsAndDrainsOnCancellation(t *testing.T) {
	queue, fake := newQueue(t)
	executor := newExecutor(t, queue, fake, jobs.ModeNormal)

	finished := make(chan struct{})
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		time.Sleep(50 * time.Millisecond)
		close(finished)
		return nil
	}))
	dispatcher := jobs.NewDispatcher(queue, fake, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{{Pool: jobs.PoolMaintenance, Capacity: 1}},
		Tick:  time.Second, Logger: quietLogger(),
	})
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		dispatcher.Run(ctx)
		close(stopped)
	}()

	fake.BlockUntilWaiters(1)
	fake.Advance(time.Second) // one tick, which claims and starts the job
	<-finished
	cancel()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func waitForQuiet(t *testing.T, dispatcher *jobs.Dispatcher) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		busy := false
		for _, count := range dispatcher.InFlight() {
			if count > 0 {
				busy = true
			}
		}
		if !busy {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("jobs were still running after 3s")
}
