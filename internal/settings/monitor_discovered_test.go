package settings_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
)

func TestCreateCategory_DefaultsToMonitorDiscovered(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if created.MonitorDiscovered != settings.MonitorDiscoveredMonitored {
		t.Errorf("want default %q, got %q", settings.MonitorDiscoveredMonitored, created.MonitorDiscovered)
	}
}

func TestSetCategoryMonitorDiscovered_RoundTrips(t *testing.T) {
	cache, _ := newCache(t)
	created, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if err := cache.SetCategoryMonitorDiscovered(t.Context(), created.ID, settings.MonitorDiscoveredNone); err != nil {
		t.Fatalf("SetCategoryMonitorDiscovered: %v", err)
	}

	category, found := cache.Snapshot().Category(created.ID)
	if !found {
		t.Fatal("category not found after update")
	}
	if category.MonitorDiscovered != settings.MonitorDiscoveredNone {
		t.Errorf("want %q, got %q", settings.MonitorDiscoveredNone, category.MonitorDiscovered)
	}
}

func TestSetCategoryMonitorDiscovered_RejectsAnUnknownPolicy(t *testing.T) {
	cache, _ := newCache(t)
	created, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if err := cache.SetCategoryMonitorDiscovered(t.Context(), created.ID, "sometimes"); err == nil {
		t.Fatal("want an error for an unknown policy value")
	}
}
