package app

import "testing"

// Every registered task's default cadence must be one the Scheduler screen can show, or the process panics at start.
func TestBuildTaskRegistry_AcceptsEveryDefault(t *testing.T) {
	registry := (&process{}).buildTaskRegistry()
	if _, found := registry.Lookup("import_retry"); !found {
		t.Error("the import retry is not registered")
	}
}
