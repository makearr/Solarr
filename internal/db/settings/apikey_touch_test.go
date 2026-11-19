package settings_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
)

// SEC-4: a key's last use is written once per interval, not once per request.
func TestTouchAPIKey_WritesOncePerInterval(t *testing.T) {
	cache, fake := newCache(t)
	user, err := cache.CreateUser(t.Context(), fake, "admin", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	key, err := cache.CreateAPIKey(t.Context(), fake, user.ID, "k", "hash", "")
	if err != nil {
		t.Fatal(err)
	}
	lastUsed := func() int64 {
		t.Helper()
		cache.FlushTouches()
		keys, err := cache.ListAPIKeys(t.Context(), user.ID)
		if err != nil || len(keys) != 1 {
			t.Fatalf("keys = %+v, %v", keys, err)
		}
		return keys[0].LastUsedAt.Unix()
	}
	start := fake.Now().Unix()
	for range 100 {
		if err := cache.TouchAPIKey(t.Context(), fake, key.ID); err != nil {
			t.Fatal(err)
		}
		fake.Advance(time.Second)
	}
	if got := lastUsed(); got != start {
		t.Errorf("last used %d after 100 touches in 100 s, want the first (%d)", got, start)
	}
	fake.Advance(6 * time.Minute)
	later := fake.Now().Unix()
	if err := cache.TouchAPIKey(t.Context(), fake, key.ID); err != nil {
		t.Fatal(err)
	}
	if got := lastUsed(); got != later {
		t.Errorf("last used %d after the interval, want %d", got, later)
	}
}
