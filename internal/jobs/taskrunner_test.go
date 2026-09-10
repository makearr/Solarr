package jobs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

// fakeScheduleSource is a settings.Cache stand-in: an in-memory override map
// plus a disabled set, matching ScheduleSource's own contract.
type fakeScheduleSource struct {
	cadence  map[string]string
	disabled map[string]bool
}

func newFakeScheduleSource() *fakeScheduleSource {
	return &fakeScheduleSource{cadence: map[string]string{}, disabled: map[string]bool{}}
}

func (f *fakeScheduleSource) Cadence(taskKey, defaultCadence string) string {
	if override, found := f.cadence[taskKey]; found {
		return override
	}
	return defaultCadence
}

func (f *fakeScheduleSource) Enabled(taskKey string) bool { return !f.disabled[taskKey] }

func newScheduleStates(t *testing.T) (*jobs.ScheduleStates, *clock.Fake) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "jobs", Path: filepath.Join(directory, "jobs.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Jobs()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return jobs.NewScheduleStates(opened), fake
}

func TestTaskRunner_FiresAndRecordsAnEnqueuedOutcome(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	fired := make(chan struct{}, 10)
	task := jobs.Task{Key: "scan", DefaultCadence: "* * * * *", Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
		fired <- struct{}{}
		return jobs.EnqueueResult{}, nil
	}}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()

	fake.BlockUntilWaiters(1)
	fake.Advance(time.Minute)

	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("the task never fired")
	}
	cancel()
	<-done

	run, found, err := states.Get(t.Context(), "scan")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || run.LastOutcome != "enqueued" || run.LastRunAt == nil {
		t.Errorf("run = %+v, found = %v, want a recorded enqueued outcome", run, found)
	}
}

func TestTaskRunner_RecordsADuplicateOutcome(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	fired := make(chan struct{}, 10)
	task := jobs.Task{Key: "capacity", DefaultCadence: "* * * * *", Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
		fired <- struct{}{}
		return jobs.EnqueueResult{Duplicate: true}, nil
	}}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	fake.BlockUntilWaiters(1)
	fake.Advance(time.Minute)
	<-fired
	cancel()
	<-done

	run, _, err := states.Get(t.Context(), "capacity")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.LastOutcome != "duplicate" {
		t.Errorf("LastOutcome = %q, want duplicate", run.LastOutcome)
	}
}

func TestTaskRunner_RecordsAnErrorOutcomeWithTheMessage(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	fired := make(chan struct{}, 10)
	failure := errors.New("db is locked")
	task := jobs.Task{Key: "probe", DefaultCadence: "* * * * *", Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
		fired <- struct{}{}
		return jobs.EnqueueResult{}, failure
	}}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	fake.BlockUntilWaiters(1)
	fake.Advance(time.Minute)
	<-fired
	cancel()
	<-done

	run, _, err := states.Get(t.Context(), "probe")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.LastOutcome != "error" || run.LastError != failure.Error() {
		t.Errorf("run = %+v, want outcome=error with the failure message", run)
	}
}

// A disabled task still ticks (so next_run_at stays accurate) but never
// calls Enqueue -- ADR-59: "a disabled task says so ... rather than being
// silent," which starts with it genuinely not running.
func TestTaskRunner_DisabledTaskTicksWithoutFiring(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	source.disabled["scan"] = true
	fired := make(chan struct{}, 10)
	task := jobs.Task{Key: "scan", DefaultCadence: "* * * * *", Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
		fired <- struct{}{}
		return jobs.EnqueueResult{}, nil
	}}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	fake.BlockUntilWaiters(1)
	fake.Advance(time.Minute)
	fake.BlockUntilWaiters(1) // the loop re-armed for the next tick without firing

	select {
	case <-fired:
		t.Fatal("a disabled task fired")
	default:
	}
	cancel()
	<-done

	run, found, err := states.Get(t.Context(), "scan")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || run.NextRunAt == nil || run.LastRunAt != nil {
		t.Errorf("run = %+v, found = %v, want next_run_at set and last_run_at still nil", run, found)
	}
}

// A cadence override changes the effective schedule without a restart --
// the whole point of resolving it live rather than once at construction.
func TestTaskRunner_UsesTheOverriddenCadence(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	source.cadence["scan"] = "*/2 * * * *" // every two minutes, not every one
	fired := make(chan struct{}, 10)
	task := jobs.Task{Key: "scan", DefaultCadence: "* * * * *", Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
		fired <- struct{}{}
		return jobs.EnqueueResult{}, nil
	}}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	fake.BlockUntilWaiters(1)
	fake.Advance(time.Minute) // one minute is not enough for a */2 schedule

	select {
	case <-fired:
		t.Fatal("fired after one minute against a two-minute override")
	case <-time.After(200 * time.Millisecond):
	}

	fake.Advance(time.Minute) // now two minutes have passed
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("did not fire after the overridden cadence elapsed")
	}
	cancel()
	<-done
}

func TestTaskRunner_RunNowFiresImmediatelyAndRefusesWhileInFlight(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	release := make(chan struct{})
	entered := make(chan struct{})
	task := jobs.Task{Key: "artwork", DefaultCadence: "0 0 1 1 *", Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
		close(entered)
		<-release
		return jobs.EnqueueResult{}, nil
	}}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	runNowDone := make(chan error, 1)
	go func() { runNowDone <- runner.RunNow(t.Context()) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first RunNow never entered Enqueue")
	}

	if err := runner.RunNow(t.Context()); !errors.Is(err, jobs.ErrAlreadyRunning) {
		t.Errorf("second RunNow = %v, want ErrAlreadyRunning", err)
	}

	close(release)
	select {
	case err := <-runNowDone:
		if err != nil {
			t.Errorf("first RunNow = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first RunNow never returned")
	}

	run, found, err := states.Get(t.Context(), "artwork")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || run.LastOutcome != "enqueued" {
		t.Errorf("run = %+v, found = %v, want a recorded enqueued outcome", run, found)
	}
}

func TestTaskRunner_StopsWhenContextEnds(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	task := jobs.Task{Key: "scan", DefaultCadence: "* * * * *", Enqueue: noopEnqueue}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()

	fake.BlockUntilWaiters(1)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}
