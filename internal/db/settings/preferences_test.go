package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestSetPreference_RoundTrips(t *testing.T) {
	cache, fake := newCache(t)
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := cache.SetPreference(t.Context(), user.ID, "library.last_level.1", `"tracks"`); err != nil {
		t.Fatalf("SetPreference: %v", err)
	}

	value, ok, err := cache.GetPreference(t.Context(), user.ID, "library.last_level.1")
	if err != nil {
		t.Fatalf("GetPreference: %v", err)
	}
	if !ok || value != `"tracks"` {
		t.Errorf("GetPreference = %q, ok=%v, want \"tracks\", true", value, ok)
	}
}

func TestGetPreference_NeverSetAnswersNotFound(t *testing.T) {
	cache, fake := newCache(t)
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, ok, err := cache.GetPreference(t.Context(), user.ID, "library.last_level.1")
	if err != nil {
		t.Fatalf("GetPreference: %v", err)
	}
	if ok {
		t.Error("ok = true, want false: this preference was never set")
	}
}

func TestSetPreference_OverwritesTheSameKey(t *testing.T) {
	cache, fake := newCache(t)
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := cache.SetPreference(t.Context(), user.ID, "library.last_level.1", `"albums"`); err != nil {
		t.Fatalf("SetPreference (first): %v", err)
	}
	if err := cache.SetPreference(t.Context(), user.ID, "library.last_level.1", `"tracks"`); err != nil {
		t.Fatalf("SetPreference (second): %v", err)
	}

	value, ok, err := cache.GetPreference(t.Context(), user.ID, "library.last_level.1")
	if err != nil {
		t.Fatalf("GetPreference: %v", err)
	}
	if !ok || value != `"tracks"` {
		t.Errorf("GetPreference = %q, ok=%v, want the overwritten \"tracks\"", value, ok)
	}
}

// Preferences are per user: one user's own value must not answer for another's.
func TestGetPreference_ScopedPerUser(t *testing.T) {
	cache, fake := newCache(t)
	first, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser (first): %v", err)
	}
	second, err := cache.CreateUser(t.Context(), fake, "listener", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser (second): %v", err)
	}

	if err := cache.SetPreference(t.Context(), first.ID, "library.last_level.1", `"albums"`); err != nil {
		t.Fatalf("SetPreference: %v", err)
	}

	_, ok, err := cache.GetPreference(t.Context(), second.ID, "library.last_level.1")
	if err != nil {
		t.Fatalf("GetPreference: %v", err)
	}
	if ok {
		t.Error("ok = true, want false: the second user never set this preference")
	}
}
