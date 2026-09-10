package settings_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
)

func TestCacheBudget_DefaultsWhenNothingIsStored(t *testing.T) {
	cache, _ := newCache(t)
	budget := cache.Snapshot().CacheBudget()
	if budget.MaxBytes != settings.DefaultCacheMaxBytes || budget.MaxAgeDays != settings.DefaultCacheMaxAgeDays ||
		budget.ImagesSharePercent != settings.DefaultCacheImagesSharePercent ||
		budget.MetadataSharePercent != settings.DefaultCacheMetadataSharePercent {
		t.Errorf("CacheBudget = %+v, want the shipped defaults", budget)
	}
}

func TestSetCacheBudget_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	want := settings.CacheBudget{
		MaxBytes: 5_000_000_000, MaxAgeDays: 30, ImagesSharePercent: 60, MetadataSharePercent: 40,
	}
	if err := cache.SetCacheBudget(t.Context(), cl, want); err != nil {
		t.Fatalf("SetCacheBudget: %v", err)
	}
	if got := cache.Snapshot().CacheBudget(); got != want {
		t.Errorf("CacheBudget = %+v, want %+v", got, want)
	}
}

func TestSetCacheBudget_RejectsSharesThatDoNotSumTo100(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetCacheBudget(t.Context(), cl, settings.CacheBudget{
		MaxBytes: 1, MaxAgeDays: 1, ImagesSharePercent: 80, MetadataSharePercent: 30,
	})
	if err == nil {
		t.Fatal("shares summing to 110 were accepted")
	}
}

func TestSetCacheBudget_RejectsNonPositiveMaxBytes(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetCacheBudget(t.Context(), cl, settings.CacheBudget{
		MaxBytes: 0, MaxAgeDays: 1, ImagesSharePercent: 80, MetadataSharePercent: 20,
	})
	if err == nil {
		t.Fatal("a zero max_bytes was accepted")
	}
}

func TestSetCacheBudget_RejectsNonPositiveMaxAgeDays(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetCacheBudget(t.Context(), cl, settings.CacheBudget{
		MaxBytes: 1, MaxAgeDays: 0, ImagesSharePercent: 80, MetadataSharePercent: 20,
	})
	if err == nil {
		t.Fatal("a zero max_age_days was accepted")
	}
}

func TestCacheMeasurement_NotFoundUntilTheTaskHasRun(t *testing.T) {
	cache, _ := newCache(t)
	_, found := cache.Snapshot().CacheMeasurement()
	if found {
		t.Error("CacheMeasurement found = true before any measurement was ever recorded")
	}
}

func TestSetCacheMeasurement_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	want := settings.CacheMeasurement{ImagesBytes: 123, MetadataBytes: 456, MeasuredAt: 1_700_000_000}
	if err := cache.SetCacheMeasurement(t.Context(), cl, want); err != nil {
		t.Fatalf("SetCacheMeasurement: %v", err)
	}
	got, found := cache.Snapshot().CacheMeasurement()
	if !found || got != want {
		t.Errorf("CacheMeasurement = %+v, found = %v, want %+v", got, found, want)
	}
}
