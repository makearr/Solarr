package settings_test

import (
	"reflect"
	"testing"
)

func TestScheduleCadence_DefaultsWhenNothingIsStored(t *testing.T) {
	cache, _ := newCache(t)
	if got := cache.Snapshot().ScheduleCadences("scan", "*/30 * * * *"); !reflect.DeepEqual(got, []string{"*/30 * * * *"}) {
		t.Errorf("ScheduleCadences = %q, want the default", got)
	}
}

func TestSetScheduleCadence_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	if err := cache.SetScheduleCadences(t.Context(), cl, "scan", []string{"0 * * * *"}); err != nil {
		t.Fatalf("SetScheduleCadences: %v", err)
	}
	if got := cache.Snapshot().ScheduleCadences("scan", "*/30 * * * *"); !reflect.DeepEqual(got, []string{"0 * * * *"}) {
		t.Errorf("ScheduleCadences = %q, want the stored override", got)
	}
}

func TestSetScheduleCadence_RejectsAnUnparseableExpression(t *testing.T) {
	cache, cl := newCache(t)
	if err := cache.SetScheduleCadences(t.Context(), cl, "scan", []string{"not a cron expression"}); err == nil {
		t.Fatal("an unparseable cadence was accepted")
	}
}

// A cron expression that parses but can never match (a calendar date that
// does not exist) is exactly as broken as one that fails to parse -- ADR-44:
// "an expression that parses but never fires is rejected at save time."
func TestSetScheduleCadence_RejectsAnExpressionThatCanNeverFire(t *testing.T) {
	cache, cl := newCache(t)
	if err := cache.SetScheduleCadences(t.Context(), cl, "scan", []string{"0 0 30 2 *"}); err == nil {
		t.Fatal("a cadence for the 30du of February was accepted")
	}
}

func TestScheduleCadence_ScopedPerTask(t *testing.T) {
	cache, cl := newCache(t)
	if err := cache.SetScheduleCadences(t.Context(), cl, "scan", []string{"0 * * * *"}); err != nil {
		t.Fatalf("SetScheduleCadences: %v", err)
	}
	if got := cache.Snapshot().ScheduleCadences("probe", "*/30 * * * *"); !reflect.DeepEqual(got, []string{"*/30 * * * *"}) {
		t.Errorf("ScheduleCadences(probe) = %q, want its own default: scan's override must not leak", got)
	}
}

func TestScheduleEnabled_DefaultsToTrue(t *testing.T) {
	cache, _ := newCache(t)
	if !cache.Snapshot().ScheduleEnabled("scan") {
		t.Error("ScheduleEnabled = false for a task nobody has touched, want true")
	}
}

func TestSetScheduleEnabled_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	if err := cache.SetScheduleEnabled(t.Context(), cl, "scan", false); err != nil {
		t.Fatalf("SetScheduleEnabled: %v", err)
	}
	if cache.Snapshot().ScheduleEnabled("scan") {
		t.Error("ScheduleEnabled = true after disabling")
	}

	if err := cache.SetScheduleEnabled(t.Context(), cl, "scan", true); err != nil {
		t.Fatalf("SetScheduleEnabled: %v", err)
	}
	if !cache.Snapshot().ScheduleEnabled("scan") {
		t.Error("ScheduleEnabled = false after re-enabling")
	}
}

// Several times of day are kept in order; a repeat or an empty list is refused.
func TestSetScheduleCadences_KeepsSeveralInOrder(t *testing.T) {
	cache, cl := newCache(t)
	want := []string{"0 11 * * *", "0 23 * * *"}
	if err := cache.SetScheduleCadences(t.Context(), cl, "scan", want); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().ScheduleCadences("scan", "*/30 * * * *"); !reflect.DeepEqual(got, want) {
		t.Errorf("ScheduleCadences = %q, want %q", got, want)
	}
	if err := cache.SetScheduleCadences(t.Context(), cl, "scan", []string{"0 11 * * *", "0 11 * * *"}); err == nil {
		t.Error("a repeated schedule was accepted")
	}
	if err := cache.SetScheduleCadences(t.Context(), cl, "scan", nil); err == nil {
		t.Error("no schedule at all was accepted")
	}
}
