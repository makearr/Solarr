package jobs_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/jobs"
)

func noopEnqueue(context.Context) (jobs.EnqueueResult, error) { return jobs.EnqueueResult{}, nil }

func TestTaskRegistry_RegisterRejectsAnEmptyKey(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	err := registry.Register(jobs.Task{Category: jobs.CategoryLibrary, DefaultCadence: "* * * * *", Enqueue: noopEnqueue})
	if err == nil {
		t.Fatal("a task with no key was accepted")
	}
}

func TestTaskRegistry_RegisterRejectsANilEnqueue(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	err := registry.Register(jobs.Task{Category: jobs.CategoryLibrary, Key: "scan", DefaultCadence: "* * * * *"})
	if err == nil {
		t.Fatal("a task with no Enqueue function was accepted")
	}
}

func TestTaskRegistry_RegisterRejectsAnInvalidCadence(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	err := registry.Register(jobs.Task{Category: jobs.CategoryLibrary, Key: "scan", DefaultCadence: "not a cron expression", Enqueue: noopEnqueue})
	if err == nil {
		t.Fatal("an invalid default cadence was accepted")
	}
}

// The user's standing rule: a default the Scheduler screen can only show as raw cron fails the build.
func TestTaskRegistry_RegisterRejectsACronOnlyDefault(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	err := registry.Register(jobs.Task{Category: jobs.CategoryLibrary, Key: "hunt", DefaultCadence: "7,22,37,52 * * * *", Enqueue: noopEnqueue})
	if err == nil {
		t.Fatal("a default cadence the Scheduler shows as Advanced (cron) was accepted")
	}
}

// The same shapes web/src/app/Scheduler.tsx's parseCadence reads as plain triggers, and some it does not.
func TestSimpleCadence(t *testing.T) {
	for cadence, want := range map[string]bool{
		"* * * * *": true, "*/15 * * * *": true, "20 * * * *": true, "0 */6 * * *": true,
		"30 2 * * *": true, "0 4 * * 0": true, "0 3 1 * *": true,
		"7,22,37,52 * * * *": false, "5-10 * * * *": false, "0 0 1 1 *": false, "*/5 2 * * *": false,
		"0 */2 * * 1": false, "0 4 * * 7": false, "0 4 1 * 1": false, "@daily": false,
	} {
		if got := jobs.SimpleCadence(cadence); got != want {
			t.Errorf("SimpleCadence(%q) = %v, want %v", cadence, got, want)
		}
	}
}

func TestTaskRegistry_RegisterRejectsADuplicateKey(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	task := jobs.Task{Category: jobs.CategoryLibrary, Key: "scan", DefaultCadence: "* * * * *", Enqueue: noopEnqueue}
	if err := registry.Register(task); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(task); err == nil {
		t.Fatal("a duplicate key was accepted")
	}
}

func TestTaskRegistry_AllListsEveryTaskSortedByKey(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	registry.MustRegister(jobs.Task{Category: jobs.CategoryLibrary, Key: "probe", DefaultCadence: "0 * * * *", Enqueue: noopEnqueue})
	registry.MustRegister(jobs.Task{Category: jobs.CategoryLibrary, Key: "artwork", DefaultCadence: "*/30 * * * *", Enqueue: noopEnqueue})

	all := registry.All()
	if len(all) != 2 || all[0].Key != "artwork" || all[1].Key != "probe" {
		t.Errorf("All() = %+v, want [artwork, probe]", all)
	}
}

func TestTaskRegistry_LookupFindsARegisteredTask(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	registry.MustRegister(jobs.Task{Category: jobs.CategoryLibrary, Key: "scan", Name: "Library scan", DefaultCadence: "*/30 * * * *", Enqueue: noopEnqueue})

	task, found := registry.Lookup("scan")
	if !found || task.Name != "Library scan" {
		t.Errorf("Lookup(scan) = %+v, %v", task, found)
	}
	if _, found := registry.Lookup("unknown"); found {
		t.Error("Lookup(unknown) found a task that was never registered")
	}
}

func TestTaskRegistry_MustRegisterPanicsOnADuplicate(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	registry.MustRegister(jobs.Task{Category: jobs.CategoryLibrary, Key: "scan", DefaultCadence: "* * * * *", Enqueue: noopEnqueue})

	defer func() {
		if recover() == nil {
			t.Error("MustRegister did not panic on a duplicate key")
		}
	}()
	registry.MustRegister(jobs.Task{Category: jobs.CategoryLibrary, Key: "scan", DefaultCadence: "* * * * *", Enqueue: noopEnqueue})
}

// A task outside the screen's groups would be shown nowhere, so it is refused.
func TestTaskRegistry_RegisterRefusesATaskWithNoCategory(t *testing.T) {
	registry := jobs.NewTaskRegistry()
	err := registry.Register(jobs.Task{Key: "scan", DefaultCadence: "* * * * *", Enqueue: noopEnqueue})
	if err == nil {
		t.Fatal("Register accepted a task with no category")
	}
}
