package musicbrainz_test

import (
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// libraryOrders is a live order per library: 0 is the default, library 2 ranks the sources the other way.
func libraryOrders(library int64) []string {
	if library == 2 {
		return []string{"second", "first"}
	}
	return []string{"first", "second"}
}

func newLibraryProvider(t *testing.T, cache metacache.Cache, sources ...musicbrainz.Source) musicbrainz.Provider {
	t.Helper()
	return musicbrainz.New(musicbrainz.Config{
		Sources: sources, LiveOrder: libraryOrders, Cache: cache, Clock: clock.NewFakeAtEpoch(),
		Logger: slog.New(slog.DiscardHandler),
	})
}

type albumCalls struct {
	group       musicbrainz.ReleaseGroup
	first       int
	second      int
	errored     bool
	description string
}

// Each library is asked in its own order, and its cached answer is never served to a library that orders the sources
// otherwise; asking again in either library is answered from that library's own entry.
func TestProvider_EachLibraryIsAnsweredInItsOwnOrderAndFromItsOwnCache(t *testing.T) {
	first := &fakeSource{name: "first", rg: musicbrainz.ReleaseGroup{MBID: "rg", Title: "From first"}, rel: musicbrainz.Release{MBID: "rel"}}
	second := &fakeSource{name: "second", rg: musicbrainz.ReleaseGroup{MBID: "rg", Title: "From second"}, rel: musicbrainz.Release{MBID: "rel"}}
	provider := newLibraryProvider(t, newTestCache(t), first, second)

	steps := []struct {
		description string
		library     int64
	}{
		{"no library", 0},
		{"library 1, the default order", 1},
		{"library 2, the other order", 2},
		{"library 2 again", 2},
		{"no library again", 0},
	}
	want := []albumCalls{
		{group: first.rg, first: 1, second: 0, description: "no library"},
		{group: first.rg, first: 1, second: 0, description: "library 1, the default order"},
		{group: second.rg, first: 1, second: 1, description: "library 2, the other order"},
		{group: second.rg, first: 1, second: 1, description: "library 2 again"},
		{group: first.rg, first: 1, second: 1, description: "no library again"},
	}
	for index, step := range steps {
		ctx := t.Context()
		if step.library != 0 {
			ctx = musicbrainz.ForLibrary(ctx, step.library)
		}
		group, _, err := provider.Album(ctx, musicbrainz.PriorityEnrich, "rg", "rel")
		got := albumCalls{group: group, first: first.calls, second: second.calls, errored: err != nil, description: step.description}
		if !reflect.DeepEqual(got, want[index]) {
			t.Errorf("step %d: got %+v, want %+v", index, got, want[index])
		}
	}
}

// An entry cached before orders were per library is served only to an order whose first source gave it: that answer is
// the one such an order asks for, whatever ranks after.
func TestProvider_AnUntaggedEntryIsServedOnlyFromTheOrdersLeadSource(t *testing.T) {
	cache := newTestCache(t)
	for kind, body := range map[string]string{
		"musicbrainz:release-group": `{"mbid":"rg","title":"Cached from first"}`,
		"musicbrainz:release":       `{"mbid":"rel"}`,
	} {
		key := map[string]string{"musicbrainz:release-group": "rg", "musicbrainz:release": "rel"}[kind]
		if err := cache.Set(t.Context(), kind, key, metacache.Entry{Source: "first", Body: body, FetchedAt: time.Unix(0, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	first := &fakeSource{name: "first", rg: musicbrainz.ReleaseGroup{MBID: "rg", Title: "From first"}, rel: musicbrainz.Release{MBID: "rel"}}
	second := &fakeSource{name: "second", rg: musicbrainz.ReleaseGroup{MBID: "rg", Title: "From second"}, rel: musicbrainz.Release{MBID: "rel"}}
	provider := newLibraryProvider(t, cache, first, second)

	group, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg", "rel")
	got := albumCalls{group: group, first: first.calls, second: second.calls, errored: err != nil}
	if want := (albumCalls{group: musicbrainz.ReleaseGroup{MBID: "rg", Title: "Cached from first"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("default order: got %+v, want %+v", got, want)
	}
	group, _, err = provider.Album(musicbrainz.ForLibrary(t.Context(), 2), musicbrainz.PriorityEnrich, "rg", "rel")
	got = albumCalls{group: group, first: first.calls, second: second.calls, errored: err != nil}
	if want := (albumCalls{group: second.rg, second: 1}); !reflect.DeepEqual(got, want) {
		t.Errorf("library 2: got %+v, want %+v", got, want)
	}
}

// The lead is the first source able to answer the lookup: a search ranked behind a source that cannot search is led by
// the first searcher, so an untagged search it answered is still served.
func TestProvider_TheLeadIsTheFirstSourceAbleToAnswer(t *testing.T) {
	cache := newTestCache(t)
	if err := cache.Set(t.Context(), "musicbrainz:search", "katupoken\x1fki pecezinu\x1f5", metacache.Entry{
		Source: "second", Body: `[{"MBID":"rg","Title":"Cached from second"}]`, FetchedAt: time.Unix(0, 0),
	}); err != nil {
		t.Fatal(err)
	}
	albumsOnly := &fakeSource{name: "first"}
	searcher := &fakeSearchSource{
		fakeSource: fakeSource{name: "second"},
		matches:    []musicbrainz.ReleaseGroupMatch{{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg", Title: "From second"}}},
	}
	provider := newLibraryProvider(t, cache, albumsOnly, searcher)

	matches, err := provider.Search(t.Context(), musicbrainz.PriorityIdentify, "Katupoken", "KI Pecezinu", 5)
	want := []musicbrainz.ReleaseGroupMatch{{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg", Title: "Cached from second"}}}
	if err != nil || !reflect.DeepEqual(matches, want) || searcher.searchCalls != 0 {
		t.Errorf("Search = %+v, %v after %d searches; want the cached answer and no search", matches, err, searcher.searchCalls)
	}
}
