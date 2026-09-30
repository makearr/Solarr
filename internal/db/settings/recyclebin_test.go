package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestRecycleBinLimits_DefaultsSaveAndRefuse(t *testing.T) {
	cache, cl := newCache(t)
	if got, want := cache.Snapshot().RecycleBinLimits(), (settings.RecycleBinLimits{MaxPercentOfRoot: 10}); got != want {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
	want := settings.RecycleBinLimits{MaxBytes: 50 << 30, MaxPercentOfRoot: 5}
	if err := cache.SetRecycleBinLimits(t.Context(), cl, want); err != nil {
		t.Fatalf("SetRecycleBinLimits: %v", err)
	}
	if got := cache.Snapshot().RecycleBinLimits(); got != want {
		t.Errorf("saved = %+v, want %+v", got, want)
	}
	for _, bad := range []settings.RecycleBinLimits{{MaxBytes: -1}, {MaxPercentOfRoot: 101}} {
		if err := cache.SetRecycleBinLimits(t.Context(), cl, bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if got := cache.Snapshot().RecycleBinLimits(); got != want {
		t.Errorf("after refusals = %+v, want %+v unchanged", got, want)
	}
}
