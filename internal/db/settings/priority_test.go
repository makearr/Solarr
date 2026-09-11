package settings_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
)

func TestPrioritySettings_DefaultsWhenNothingIsStored(t *testing.T) {
	cache, _ := newCache(t)
	got := cache.Snapshot().PrioritySettings()
	want := settings.PrioritySettings{
		UserRequestWeight: settings.DefaultPriorityUserRequestWeight,
		NewReleaseWeight:  settings.DefaultPriorityNewReleaseWeight,
		GapWeight:         settings.DefaultPriorityGapWeight,
		UpgradeWeight:     settings.DefaultPriorityUpgradeWeight,
		NewReleaseDays:    settings.DefaultPriorityNewReleaseDays,
	}
	if got != want {
		t.Errorf("PrioritySettings = %+v, want the shipped defaults %+v", got, want)
	}
}

func TestSetPrioritySettings_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	want := settings.PrioritySettings{
		UserRequestWeight: 50, NewReleaseWeight: 35, GapWeight: 15, UpgradeWeight: 5, NewReleaseDays: 14,
	}
	if err := cache.SetPrioritySettings(t.Context(), cl, want); err != nil {
		t.Fatalf("SetPrioritySettings: %v", err)
	}
	if got := cache.Snapshot().PrioritySettings(); got != want {
		t.Errorf("PrioritySettings = %+v, want %+v", got, want)
	}
}

func TestSetPrioritySettings_RejectsANegativeWeight(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetPrioritySettings(t.Context(), cl, settings.PrioritySettings{
		UserRequestWeight: -1, NewReleaseWeight: 30, GapWeight: 20, UpgradeWeight: 10, NewReleaseDays: 7,
	})
	if err == nil {
		t.Fatal("a negative weight was accepted")
	}
}

func TestSetPrioritySettings_RejectsNonPositiveNewReleaseDays(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetPrioritySettings(t.Context(), cl, settings.PrioritySettings{
		UserRequestWeight: 40, NewReleaseWeight: 30, GapWeight: 20, UpgradeWeight: 10, NewReleaseDays: 0,
	})
	if err == nil {
		t.Fatal("a zero new_release_days was accepted")
	}
}
