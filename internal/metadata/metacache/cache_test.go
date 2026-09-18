package metacache_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func newCache(t *testing.T) metacache.Cache {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "metadata", Path: filepath.Join(directory, "metadata.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Metadata()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return metacache.New(opened)
}

func TestCache_MissReportsErrNotFound(t *testing.T) {
	cache := newCache(t)

	_, err := cache.Get(t.Context(), "release", "04e9a8bd-175f-44a1-83a0-17d5e9fbd116")
	if !errors.Is(err, metacache.ErrNotFound) {
		t.Fatalf("Get on an empty cache = %v, want ErrNotFound", err)
	}
}

func TestCache_SetThenGetRoundTrips(t *testing.T) {
	cache := newCache(t)
	at := time.Unix(1_700_000_000, 0)

	err := cache.Set(t.Context(), "release-group", "b139", metacache.Entry{
		Source: "musicbrainz", Body: `{"title":"KI Pecezinu"}`, FetchedAt: at,
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := cache.Get(t.Context(), "release-group", "b139")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Source != "musicbrainz" || got.Body != `{"title":"KI Pecezinu"}` || !got.FetchedAt.Equal(at) {
		t.Errorf("Get = %+v, want the stored entry", got)
	}
}

// A cache entry a proxy source wrote must be readable by the direct source's own
// lookup and vice versa (ADR-29's failover) -- the key is (kind, external id) only,
// never which provider answered.
func TestCache_KeyDoesNotIncludeSource(t *testing.T) {
	cache := newCache(t)
	at := time.Unix(1_700_000_000, 0)

	if err := cache.Set(t.Context(), "release", "fba5", metacache.Entry{
		Source: "musicbrainz-proxy", Body: `{"title":"first"}`, FetchedAt: at,
	}); err != nil {
		t.Fatalf("Set (proxy): %v", err)
	}
	if err := cache.Set(t.Context(), "release", "fba5", metacache.Entry{
		Source: "musicbrainz", Body: `{"title":"second"}`, FetchedAt: at,
	}); err != nil {
		t.Fatalf("Set (direct): %v", err)
	}

	got, err := cache.Get(t.Context(), "release", "fba5")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Source != "musicbrainz" || got.Body != `{"title":"second"}` {
		t.Errorf("Get = %+v, want the second write to have replaced the first in place", got)
	}
}

// Two different kinds under the same external id (unlikely, but the schema allows
// it) must not collide -- kind is part of the key, not a namespace prefix folded
// into it.
func TestCache_KindDistinguishesTheSameExternalID(t *testing.T) {
	cache := newCache(t)
	at := time.Unix(1_700_000_000, 0)

	if err := cache.Set(t.Context(), "release-group", "shared", metacache.Entry{
		Source: "musicbrainz", Body: "rg", FetchedAt: at,
	}); err != nil {
		t.Fatalf("Set (release-group): %v", err)
	}
	if err := cache.Set(t.Context(), "release", "shared", metacache.Entry{
		Source: "musicbrainz", Body: "release", FetchedAt: at,
	}); err != nil {
		t.Fatalf("Set (release): %v", err)
	}

	rg, err := cache.Get(t.Context(), "release-group", "shared")
	if err != nil {
		t.Fatalf("Get (release-group): %v", err)
	}
	if rg.Body != "rg" {
		t.Errorf("release-group body = %q, want %q", rg.Body, "rg")
	}
}

func TestCache_SizeBytesSumsEveryEntry(t *testing.T) {
	cache := newCache(t)
	at := time.Unix(1_700_000_000, 0)

	if err := cache.Set(t.Context(), "release", "a", metacache.Entry{Body: "12345", FetchedAt: at}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Set(t.Context(), "release", "b", metacache.Entry{Body: "1234567890", FetchedAt: at}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	size, err := cache.SizeBytes(t.Context())
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size != 15 {
		t.Errorf("SizeBytes = %d, want 15 (5+10)", size)
	}
}

func TestCache_SizeBytesIsZeroWhenEmpty(t *testing.T) {
	cache := newCache(t)
	size, err := cache.SizeBytes(t.Context())
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size != 0 {
		t.Errorf("SizeBytes = %d, want 0 for an empty cache", size)
	}
}

func TestCache_DeleteOlderThanRemovesOnlyStaleEntries(t *testing.T) {
	cache := newCache(t)
	old := time.Unix(1_000_000_000, 0)
	recent := time.Unix(1_700_000_000, 0)

	if err := cache.Set(t.Context(), "release", "old", metacache.Entry{Body: "x", FetchedAt: old}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Set(t.Context(), "release", "recent", metacache.Entry{Body: "x", FetchedAt: recent}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	removed, err := cache.DeleteOlderThan(t.Context(), time.Unix(1_500_000_000, 0))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := cache.Get(t.Context(), "release", "old"); !errors.Is(err, metacache.ErrNotFound) {
		t.Error("the old entry survived DeleteOlderThan")
	}
	if _, err := cache.Get(t.Context(), "release", "recent"); err != nil {
		t.Error("the recent entry was removed by DeleteOlderThan")
	}
}

func TestCache_DeleteOldestRemovesTheOldestFirst(t *testing.T) {
	cache := newCache(t)
	for index, id := range []string{"oldest", "middle", "newest"} {
		at := time.Unix(1_000_000_000+int64(index)*1000, 0)
		if err := cache.Set(t.Context(), "release", id, metacache.Entry{Body: "x", FetchedAt: at}); err != nil {
			t.Fatalf("Set(%s): %v", id, err)
		}
	}

	removed, err := cache.DeleteOldest(t.Context(), 2)
	if err != nil {
		t.Fatalf("DeleteOldest: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if _, err := cache.Get(t.Context(), "release", "oldest"); !errors.Is(err, metacache.ErrNotFound) {
		t.Error("oldest survived")
	}
	if _, err := cache.Get(t.Context(), "release", "middle"); !errors.Is(err, metacache.ErrNotFound) {
		t.Error("middle survived")
	}
	if _, err := cache.Get(t.Context(), "release", "newest"); err != nil {
		t.Error("newest was removed, want it kept")
	}
}

func TestCache_DeleteOldestNeverRemovesMoreThanExist(t *testing.T) {
	cache := newCache(t)
	if err := cache.Set(t.Context(), "release", "only", metacache.Entry{Body: "x", FetchedAt: time.Unix(1, 0)}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	removed, err := cache.DeleteOldest(t.Context(), 100)
	if err != nil {
		t.Fatalf("DeleteOldest: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (capped at what actually exists)", removed)
	}
}

func TestCache_ClearRemovesEverything(t *testing.T) {
	cache := newCache(t)
	if err := cache.Set(t.Context(), "release", "a", metacache.Entry{Body: "x", FetchedAt: time.Unix(1, 0)}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Set(t.Context(), "release", "b", metacache.Entry{Body: "x", FetchedAt: time.Unix(1, 0)}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := cache.Clear(t.Context()); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	count, err := cache.Count(t.Context())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("Count = %d after Clear, want 0", count)
	}
}
