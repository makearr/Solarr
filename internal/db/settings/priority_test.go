package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
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
	if got.SearchesPerHour == nil || *got.SearchesPerHour != settings.DefaultSearchesPerHour {
		t.Errorf("SearchesPerHour = %v, want %d", got.SearchesPerHour, settings.DefaultSearchesPerHour)
	}
	if got.SearchesPerHour = nil; got != want {
		t.Errorf("PrioritySettings = %+v, want the shipped defaults %+v", got, want)
	}
}

func TestSetPrioritySettings_KeepsTheSearchBudgetWhenLeftOut(t *testing.T) {
	cache, cl := newCache(t)
	three := int64(3)
	base := settings.PrioritySettings{UserRequestWeight: 40, NewReleaseWeight: 30, GapWeight: 20, UpgradeWeight: 10, NewReleaseDays: 7}
	withBudget := base
	withBudget.SearchesPerHour = &three
	if err := cache.SetPrioritySettings(t.Context(), cl, withBudget); err != nil {
		t.Fatalf("SetPrioritySettings: %v", err)
	}
	if err := cache.SetPrioritySettings(t.Context(), cl, base); err != nil {
		t.Fatalf("SetPrioritySettings: %v", err)
	}
	if got := cache.Snapshot().PrioritySettings().SearchesPerHour; got == nil || *got != 3 {
		t.Errorf("SearchesPerHour = %v, want 3 kept", got)
	}
	tooMany := int64(5000)
	withBudget.SearchesPerHour = &tooMany
	if err := cache.SetPrioritySettings(t.Context(), cl, withBudget); err == nil {
		t.Error("SetPrioritySettings accepted 5000 searches an hour")
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
	got := cache.Snapshot().PrioritySettings()
	if got.SearchesPerHour = nil; got != want {
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
