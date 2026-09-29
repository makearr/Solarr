package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/jobs"
)

// 2026-09-29: the status bar lists a job while it runs, with its progress, and forgets it after.
func TestRunning_ListsAJobWhileItRunsWithItsRateAndTimeLeft(t *testing.T) {
	queue, fake := newQueue(t)
	running := jobs.NewRunning(fake)
	executor := jobs.NewExecutor(queue, fake, jobs.ExecutorConfig{
		Owner: "worker-1", LeaseTTL: time.Minute, Logger: quietLogger(), Running: running,
	})

	var during []jobs.RunningJob
	executor.Handle(typeBackup, jobs.HandlerFunc(func(_ context.Context, job jobs.Job) error {
		job.Progress.Set(0, 100)
		fake.Advance(30 * time.Second)
		job.Progress.Add(10)
		fake.Advance(30 * time.Second)
		job.Progress.Add(10)
		during = running.Snapshot()
		return nil
	}))
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	// 20 done in a minute, 80 left: four minutes.
	want := []jobs.RunningJob{{
		JobID: enqueued.ID, Type: typeBackup, StartedAt: fake.Now().Add(-time.Minute), Done: 20, Total: 100,
		PerMinute: 20, Remaining: 4 * time.Minute,
	}}
	if len(during) != 1 || during[0] != want[0] {
		t.Errorf("while running = %+v\nwant %+v", during, want)
	}
	if after := running.Snapshot(); len(after) != 0 {
		t.Errorf("after finishing = %+v, want nothing", after)
	}
}

// The rate is the last five minutes', so a job that sped up is estimated at its new pace.
func TestRunning_TheRateForgetsWorkOlderThanFiveMinutes(t *testing.T) {
	queue, fake := newQueue(t)
	running := jobs.NewRunning(fake)
	executor := jobs.NewExecutor(queue, fake, jobs.ExecutorConfig{
		Owner: "worker-1", LeaseTTL: time.Hour, Logger: quietLogger(), Running: running,
	})

	var during []jobs.RunningJob
	executor.Handle(typeBackup, jobs.HandlerFunc(func(_ context.Context, job jobs.Job) error {
		job.Progress.Set(0, 1000)
		for range 10 { // ten slow minutes: one a minute
			fake.Advance(time.Minute)
			job.Progress.Add(1)
		}
		for range 5 { // five fast ones: ten a minute
			fake.Advance(time.Minute)
			job.Progress.Add(10)
		}
		during = running.Snapshot()
		return nil
	}))
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	if len(during) != 1 || during[0].PerMinute != 10 || during[0].Remaining != 94*time.Minute {
		t.Errorf("while running = %+v, want 10 a minute and 94 minutes left", during)
	}
}

// Without a total, or before there is history, nothing is estimated.
func TestProgress_NoEstimateWithoutATotalOrHistory(t *testing.T) {
	queue, fake := newQueue(t)
	running := jobs.NewRunning(fake)
	executor := jobs.NewExecutor(queue, fake, jobs.ExecutorConfig{
		Owner: "worker-1", LeaseTTL: time.Minute, Logger: quietLogger(), Running: running,
	})

	var fresh, unbounded []jobs.RunningJob
	executor.Handle(typeBackup, jobs.HandlerFunc(func(_ context.Context, job jobs.Job) error {
		job.Progress.Set(5, 100)
		fresh = running.Snapshot()
		job.Progress.Set(5, 0)
		fake.Advance(time.Minute)
		job.Progress.Add(5)
		unbounded = running.Snapshot()
		return nil
	}))
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	executor.Execute(t.Context(), claimOne(t, queue, jobs.PoolMaintenance, "worker-1"))

	if len(fresh) != 1 || fresh[0].PerMinute != 0 || fresh[0].Remaining != 0 {
		t.Errorf("with no history = %+v, want no rate", fresh)
	}
	if len(unbounded) != 1 || unbounded[0].Remaining != 0 || unbounded[0].Done != 10 {
		t.Errorf("with no total = %+v, want 10 done and no estimate", unbounded)
	}
}
