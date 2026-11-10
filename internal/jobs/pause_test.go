package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/jobs"
)

func newPausedExecutor(t *testing.T) (*jobs.Queue, *jobs.Pauses, *jobs.Executor) {
	t.Helper()
	queue, opened, fake := newQueueWithStore(t)
	pauses := jobs.NewPauses(opened, fake)
	executor := jobs.NewExecutor(queue, fake, jobs.ExecutorConfig{
		Owner: "worker-1", Mode: jobs.ModeNormal, LeaseTTL: time.Minute, Logger: quietLogger(), Pauses: pauses,
	})
	return queue, pauses, executor
}

func TestPause_AHeldTasksJobsAreNotClaimedUntilResumed(t *testing.T) {
	queue, pauses, _ := newPausedExecutor(t)
	held := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, ActionKey: "held"})
	_ = held
	taskJob, err := queue.Enqueue(jobs.WithTaskKey(t.Context(), "backup"), jobs.Request{Type: typeBackup, ActionKey: "task"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := pauses.Pause(t.Context(), "backup"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 10, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ActionKey != "held" {
		t.Fatalf("claimed %+v while paused, want only the job no task started", claimed)
	}
	if total, _ := queue.ClaimableIn(t.Context(), jobs.PoolMaintenance); total != 0 {
		t.Errorf("ClaimableIn = %d while paused, want 0", total)
	}
	if counts, _ := pauses.Held(t.Context()); counts["backup"] != 1 {
		t.Errorf("Held = %v, want one job waiting for backup", counts)
	}

	if err := pauses.Resume(t.Context(), "backup"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	claimed, err = queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 10, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != taskJob.Job.ID {
		t.Fatalf("after Resume claimed %+v, %v, want the task's job", claimed, err)
	}
}

func TestPause_AllTasksHoldsEveryTaskButNotOtherWork(t *testing.T) {
	queue, pauses, _ := newPausedExecutor(t)
	if _, err := queue.Enqueue(jobs.WithTaskKey(t.Context(), "scan"), jobs.Request{Type: typeBackup, ActionKey: "a"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup, ActionKey: "b"})

	if err := pauses.Pause(t.Context(), jobs.AllTasks); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	claimed, _ := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 10, time.Minute)
	if len(claimed) != 1 || claimed[0].ActionKey != "b" {
		t.Fatalf("claimed %+v under a pause of all, want only the job no task started", claimed)
	}
	if !pauses.IsPaused("scan") || pauses.IsPaused("") {
		t.Errorf("IsPaused(scan) = %v, IsPaused(none) = %v, want true and false", pauses.IsPaused("scan"), pauses.IsPaused(""))
	}
}

func TestPause_StopsARunningJobAndPutsItBackWithoutSpendingAnAttempt(t *testing.T) {
	queue, pauses, executor := newPausedExecutor(t)
	started := make(chan struct{})
	executor.Handle(typeBackup, jobs.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}))
	enqueued, err := queue.Enqueue(jobs.WithTaskKey(t.Context(), "backup"), jobs.Request{Type: typeBackup})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		executor.Execute(context.Background(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))
	}()
	<-started

	if err := pauses.Pause(t.Context(), "backup"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the running job was not stopped by the pause")
	}

	waiting, err := queue.Get(t.Context(), enqueued.Job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if waiting.State != jobs.StatePending || waiting.Attempts != 0 || waiting.LastError != "paused" {
		t.Errorf("job = state %q, attempts %d, error %q, want PENDING, 0, \"paused\"", waiting.State, waiting.Attempts, waiting.LastError)
	}
	if claimed, _ := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 1, time.Minute); len(claimed) != 0 {
		t.Errorf("claimed %+v while paused", claimed)
	}
}

func TestPause_AJobClaimedJustBeforeThePauseDoesNotRun(t *testing.T) {
	queue, pauses, executor := newPausedExecutor(t)
	ran := false
	executor.Handle(typeBackup, jobs.HandlerFunc(func(context.Context, jobs.Job) error {
		ran = true
		return nil
	}))
	enqueued, err := queue.Enqueue(jobs.WithTaskKey(t.Context(), "backup"), jobs.Request{Type: typeBackup})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed := claimOne(t, queue, jobs.PoolMaintenance, "worker-1")
	if err := pauses.Pause(t.Context(), "backup"); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	executor.Execute(t.Context(), claimed)

	waiting, _ := queue.Get(t.Context(), enqueued.Job.ID)
	if ran || waiting.State != jobs.StatePending {
		t.Errorf("ran = %v, state = %q, want the handler skipped and the job PENDING", ran, waiting.State)
	}
}

func TestPause_ChildJobsAndContinuationsStayWithTheirTask(t *testing.T) {
	queue, pauses, executor := newPausedExecutor(t)
	executor.Handle(typeBackup, jobs.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		_, err := queue.Enqueue(ctx, jobs.Request{Type: typeBackup, ActionKey: "child"})
		return errors.Join(err, jobs.ErrYield)
	}))
	if _, err := queue.Enqueue(jobs.WithTaskKey(t.Context(), "scan"), jobs.Request{Type: typeBackup, ActionKey: "parent"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	if err := pauses.Pause(t.Context(), "scan"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if claimed, _ := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 10, time.Minute); len(claimed) != 0 {
		t.Errorf("claimed %+v, want the child and the continuation held by the scan's pause", claimed)
	}
	if counts, _ := pauses.Held(t.Context()); counts["scan"] != 2 {
		t.Errorf("Held = %v, want 2 jobs waiting for scan", counts)
	}
}

func TestPause_SurvivesARestart(t *testing.T) {
	_, opened, fake := newQueueWithStore(t)
	first := jobs.NewPauses(opened, fake)
	if err := first.Pause(t.Context(), "scan"); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	second := jobs.NewPauses(opened, fake)
	if second.IsPaused("scan") {
		t.Fatal("a new Pauses knows a pause before Load")
	}
	if err := second.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if at, found := second.PausedAt("scan"); !found || !at.Equal(fake.Now().Truncate(time.Second)) {
		t.Errorf("PausedAt = %v, %v, want the time it was paused", at, found)
	}
}
