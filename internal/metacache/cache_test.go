package metacache_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/metacache"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
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
