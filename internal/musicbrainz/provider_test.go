package musicbrainz_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/metacache"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

func newTestCache(t *testing.T) metacache.Cache {
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

// fakeSource is a Source that answers or fails on demand and counts its own calls,
// so a test can assert both the outcome and which sources were actually asked.
type fakeSource struct {
	name  string
	rg    musicbrainz.ReleaseGroup
	rel   musicbrainz.Release
	err   error
	calls int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Album(context.Context, string, string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	f.calls++
	if f.err != nil {
		return musicbrainz.ReleaseGroup{}, musicbrainz.Release{}, f.err
	}
	return f.rg, f.rel, nil
}

func newProvider(t *testing.T, sources ...musicbrainz.Source) musicbrainz.Provider {
	t.Helper()
	return musicbrainz.New(musicbrainz.Config{
		Sources: sources, Cache: newTestCache(t), Clock: clock.NewFakeAtEpoch(),
		Logger: slog.New(slog.DiscardHandler),
	})
}

func TestProvider_FallsThroughToTheNextSourceOnFailure(t *testing.T) {
	proxy := &fakeSource{name: "proxy", err: errors.New("proxy down")}
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1"}}
	provider := newProvider(t, proxy, direct)

	rg, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	if rg.Title != "KI Pecezinu" {
		t.Errorf("Title = %q, want the fallback source's answer", rg.Title)
	}
	if proxy.calls != 1 || direct.calls != 1 {
		t.Errorf("calls: proxy=%d direct=%d, want exactly one each", proxy.calls, direct.calls)
	}
}

func TestProvider_ErrorsWhenEverySourceFails(t *testing.T) {
	proxy := &fakeSource{name: "proxy", err: errors.New("proxy down")}
	direct := &fakeSource{name: "direct", err: errors.New("direct down too")}
	provider := newProvider(t, proxy, direct)

	_, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1")
	if err == nil {
		t.Fatal("Album succeeded although both sources failed")
	}
}

// The whole point of the permanent cache (ADR-29): a second lookup for the same
// pair must not touch a source at all.
func TestProvider_ASecondLookupIsServedFromCache(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1", Title: "KI Pecezinu"}}
	provider := newProvider(t, direct)

	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("first Album: %v", err)
	}
	rg, release, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1")
	if err != nil {
		t.Fatalf("second Album: %v", err)
	}
	if direct.calls != 1 {
		t.Errorf("source called %d times, want exactly 1 -- the second lookup should hit the cache", direct.calls)
	}
	if rg.Title != "KI Pecezinu" || release.Title != "KI Pecezinu" {
		t.Errorf("cached answer = %+v / %+v, want the first lookup's result round-tripped", rg, release)
	}
}

// A second edition of the same release group has a cache hit on the release group
// half only. That must not serve the first edition's cached Release for the second
// edition's own, different release id.
func TestProvider_APartialCacheHitStillFetchesTheGenuineMiss(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1"}}
	provider := newProvider(t, direct)

	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("first Album: %v", err)
	}

	direct.rel = musicbrainz.Release{MBID: "rel2"}
	_, release, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel2")
	if err != nil {
		t.Fatalf("second Album: %v", err)
	}
	if direct.calls != 2 {
		t.Errorf("source called %d times, want 2 -- rg1/rel2 was a genuine miss", direct.calls)
	}
	if release.MBID != "rel2" {
		t.Errorf("Release = %+v, want rel2's own data, not rel1's cached entry", release)
	}
}
