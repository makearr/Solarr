package adapters_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ADR-26 (todo 4.14): "a setting not in the registry cannot be written."
// An empty registry stands in for a write path that forgot to register its
// own key -- the mutation the Registry.LookupAll check in
// CacheManager.SetBudget guards against. Cache is deliberately left nil:
// a correct guard never reaches it.
func TestCacheManager_SetBudget_RefusesWhenCacheKeysAreNotRegistered(t *testing.T) {
	manager := adapters.CacheManager{Registry: settings.NewRegistry()}

	err := manager.SetBudget(t.Context(), apiserver.CacheBudget{
		MaxBytes: 1, MaxAgeDays: 1, ImagesSharePercent: 80, MetadataSharePercent: 20,
	})
	if !errors.Is(err, settings.ErrSettingNotRegistered) {
		t.Fatalf("SetBudget error = %v, want ErrSettingNotRegistered", err)
	}
}
