package jobs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// fakeScheduleSource is a settings.Cache stand-in: an in-memory override map
// plus a disabled set, matching ScheduleSource's own contract.
type fakeScheduleSource struct {
	cadence  map[string][]string
	disabled map[string]bool
	startup  map[string]bool
}

func newFakeScheduleSource() *fakeScheduleSource {
	return &fakeScheduleSource{cadence: map[string][]string{}, disabled: map[string]bool{}, startup: map[string]bool{}}
}

func (f *fakeScheduleSource) Cadences(taskKey, defaultCadence string) []string {
	if override, found := f.cadence[taskKey]; found {
		return override
	}
	return []string{defaultCadence}
}

func (f *fakeScheduleSource) RunAtStartup(taskKey string, fallback bool) bool {
	if run, found := f.startup[taskKey]; found {
		return run
	}
	return fallback
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
	source.cadence["scan"] = []string{"*/2 * * * *"} // every two minutes, not every one
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

// The full-rescan trigger (private/questions.md): a task with no RunFull refuses rather than
// silently falling back to its ordinary Enqueue.
func TestTaskRunner_RunNowFullRefusesForATaskWithNoFullMode(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	task := jobs.Task{Key: "enrichment", DefaultCadence: "* * * * *", Enqueue: noopEnqueue}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	if err := runner.RunNowFull(t.Context()); !errors.Is(err, jobs.ErrNoFullMode) {
		t.Errorf("RunNowFull = %v, want ErrNoFullMode", err)
	}
}

// RunNowFull calls the task's own RunFull, not Enqueue, and shares RunNow's own running lock --
// a full rescan and an ordinary run of the same task must never overlap.
func TestTaskRunner_RunNowFullFiresRunFullAndSharesTheRunningLock(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	release := make(chan struct{})
	entered := make(chan struct{})
	task := jobs.Task{
		Key: "scan", DefaultCadence: "* * * * *",
		Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
			t.Error("RunNowFull called Enqueue instead of RunFull")
			return jobs.EnqueueResult{}, nil
		},
		RunFull: func(context.Context) (jobs.EnqueueResult, error) {
			close(entered)
			<-release
			return jobs.EnqueueResult{}, nil
		},
	}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	runNowFullDone := make(chan error, 1)
	go func() { runNowFullDone <- runner.RunNowFull(t.Context()) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("RunNowFull never entered RunFull")
	}

	if err := runner.RunNow(t.Context()); !errors.Is(err, jobs.ErrAlreadyRunning) {
		t.Errorf("RunNow while RunNowFull is in flight = %v, want ErrAlreadyRunning", err)
	}

	close(release)
	select {
	case err := <-runNowFullDone:
		if err != nil {
			t.Errorf("RunNowFull = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunNowFull never returned")
	}

	run, found, err := states.Get(t.Context(), "scan")
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

// Several schedules fire at whichever comes first ("11:00 and 23:00", 2026-09-19 review).
func TestTaskRunner_FiresAtTheEarliestOfSeveralCadences(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	source.cadence["scan"] = []string{"0 23 * * *", "0 11 * * *"}
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
	fake.Advance(11 * time.Hour)
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("did not fire at 11:00, the earlier of the two schedules")
	}
	cancel()
	<-done
}

// Startup fires a task that has it on, by default or by the user's choice, and is enabled.
func TestTaskRunner_StartupFiresOnlyWhenOnAndEnabled(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	source.startup["switched_on"] = true
	source.startup["zero_value_switched_on"] = true
	source.disabled["disabled"] = true

	var fired []string
	for _, task := range []jobs.Task{
		{Key: "default_on", Startup: jobs.StartupOn},
		{Key: "default_off", Startup: jobs.StartupOff},
		{Key: "switched_on", Startup: jobs.StartupOff},
		{Key: "zero_value"},
		{Key: "zero_value_switched_on"},
		{Key: "disabled", Startup: jobs.StartupOn},
	} {
		task.DefaultCadence = "0 0 1 1 *"
		task.Enqueue = func(context.Context) (jobs.EnqueueResult, error) {
			fired = append(fired, task.Key)
			return jobs.EnqueueResult{}, nil
		}
		jobs.NewTaskRunner(task, source, states, fake, quietLogger()).Startup(t.Context())
	}
	if want := []string{"default_on", "switched_on", "zero_value_switched_on"}; !reflect.DeepEqual(fired, want) {
		t.Errorf("fired %v, want %v", fired, want)
	}
}

// "Run now" is a person asking, so it fires EnqueueManual; a startup run is not, so it fires Enqueue.
func TestTaskRunner_RunNowFiresEnqueueManualAndStartupDoesNot(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	var fired []string
	task := jobs.Task{
		Key: "enrichment", DefaultCadence: "* * * * *", Startup: jobs.StartupOn,
		Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
			fired = append(fired, "scheduled")
			return jobs.EnqueueResult{}, nil
		},
		EnqueueManual: func(context.Context) (jobs.EnqueueResult, error) {
			fired = append(fired, "manual")
			return jobs.EnqueueResult{}, nil
		},
	}
	runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

	runner.Startup(t.Context())
	if err := runner.RunNow(t.Context()); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if want := []string{"scheduled", "manual"}; !slices.Equal(fired, want) {
		t.Errorf("fired %v, want %v", fired, want)
	}
}

// A heavy task's startup run waits out its delay, and is skipped altogether when the task ran within its own interval:
// a restart two days after the weekly maintenance must not repeat it.
func TestTaskRunner_HeavyStartupWaitsAndIsSkippedAfterARecentRun(t *testing.T) {
	cases := []struct {
		name    string
		lastRan time.Duration // how long before now the task last ran; zero is never
		want    int
	}{
		{"never ran", 0, 1},
		{"ran two days ago, weekly", 48 * time.Hour, 0},
		{"ran eight days ago, weekly", 8 * 24 * time.Hour, 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			states, fake := newScheduleStates(t)
			source := newFakeScheduleSource()
			fired := 0
			task := jobs.Task{
				Key: "maintenance", DefaultCadence: "0 4 * * 0", Startup: jobs.StartupOn, StartupDelay: 5 * time.Minute,
				Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
					fired++
					return jobs.EnqueueResult{}, nil
				},
			}
			if test.lastRan > 0 {
				ranAt := fake.Now().Add(-test.lastRan)
				if err := states.RecordRun(t.Context(), task.Key, ranAt, time.Second, "enqueued", "", fake.Now()); err != nil {
					t.Fatalf("RecordRun: %v", err)
				}
			}
			runner := jobs.NewTaskRunner(task, source, states, fake, quietLogger())

			done := make(chan struct{})
			go func() {
				defer close(done)
				runner.Startup(t.Context())
			}()
			if test.want == 1 {
				fake.BlockUntilWaiters(1)
				if fired != 0 {
					t.Fatal("fired before the startup delay had passed")
				}
				fake.Advance(5 * time.Minute)
			}
			<-done
			if fired != test.want {
				t.Errorf("fired %d times, want %d", fired, test.want)
			}
		})
	}
}

// The hunt spreads its hourly budget by its own cadence; an unparseable override falls back to the default.
func TestCadenceIntervalIsTheGapBetweenFirings(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 7, 0, 0, time.UTC)
	for _, test := range []struct {
		cadences []string
		want     time.Duration
	}{
		{[]string{"*/20 * * * *"}, 20 * time.Minute},
		{[]string{"0 * * * *"}, time.Hour},
		{[]string{"not a cadence"}, 15 * time.Minute},
		{nil, 15 * time.Minute},
	} {
		if got := jobs.CadenceInterval(test.cadences, "*/15 * * * *", now); got != test.want {
			t.Errorf("CadenceInterval(%v) = %v, want %v", test.cadences, got, test.want)
		}
	}
}

// A cadence changed while the runner sleeps takes effect within a minute: a weekly task made every-minute does not wait out
// the week first.
func TestTaskRunner_ACadenceChangedWhileAsleepTakesEffect(t *testing.T) {
	states, fake := newScheduleStates(t)
	source := newFakeScheduleSource()
	source.cadence["sweep"] = []string{"0 0 * * 0"}
	fired := make(chan struct{}, 10)
	task := jobs.Task{Key: "sweep", DefaultCadence: "0 0 * * 0", Enqueue: func(context.Context) (jobs.EnqueueResult, error) {
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
	defer func() {
		cancel()
		<-done
	}()

	fake.BlockUntilWaiters(1)
	source.cadence["sweep"] = []string{"* * * * *"}
	fake.Advance(time.Minute)
	fake.BlockUntilWaiters(1)
	fake.Advance(time.Minute)

	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("the task kept sleeping toward its old weekly time")
	}
}
