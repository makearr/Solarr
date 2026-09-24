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
