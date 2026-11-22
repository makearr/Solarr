package musicbrainz_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/migrations"
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

// fakeSearchSource is a fakeSource that also implements Searcher, so Provider.Search
// has something in the source list that qualifies for the type assertion.
type fakeSearchSource struct {
	fakeSource
	matches     []musicbrainz.ReleaseGroupMatch
	searchErr   error
	searchCalls int
}

func (f *fakeSearchSource) SearchReleaseGroups(
	context.Context, string, string, int,
) ([]musicbrainz.ReleaseGroupMatch, error) {
	f.searchCalls++
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.matches, nil
}

// fakeQuerySearchSource is a fakeSource that also implements QuerySearcher, so
// Provider.SearchByQuery has something in the source list that qualifies.
type fakeQuerySearchSource struct {
	fakeSource
	matches     []musicbrainz.ReleaseGroupMatch
	searchErr   error
	searchCalls int
}

func (f *fakeQuerySearchSource) SearchReleaseGroupsByQuery(
	context.Context, string, int,
) ([]musicbrainz.ReleaseGroupMatch, error) {
	f.searchCalls++
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.matches, nil
}

// fakeArtistLookupSource is a fakeSource that also implements ArtistLookup, so
// Provider.Artist has something in the source list that qualifies.
type fakeArtistLookupSource struct {
	fakeSource
	artist      musicbrainz.Artist
	artistErr   error
	artistCalls int
}

func (f *fakeArtistLookupSource) Artist(context.Context, string) (musicbrainz.Artist, error) {
	f.artistCalls++
	if f.artistErr != nil {
		return musicbrainz.Artist{}, f.artistErr
	}
	return f.artist, nil
}

type fakeDiscographySource struct {
	fakeSource
	groups           []musicbrainz.ReleaseGroup
	discographyErr   error
	discographyCalls int
	sawBootlegOnly   []bool
}

func (f *fakeDiscographySource) ArtistReleaseGroups(ctx context.Context, _ string) ([]musicbrainz.ReleaseGroup, error) {
	f.discographyCalls++
	f.sawBootlegOnly = append(f.sawBootlegOnly, musicbrainz.IncludesBootlegOnlyGroups(ctx))
	if f.discographyErr != nil {
		return nil, f.discographyErr
	}
	return f.groups, nil
}

func newProvider(t *testing.T, sources ...musicbrainz.Source) musicbrainz.Provider {
	t.Helper()
	return musicbrainz.New(musicbrainz.Config{
		Sources: sources, Cache: newTestCache(t), Clock: clock.NewFakeAtEpoch(),
		Logger: slog.New(slog.DiscardHandler),
	})
}

// newRefreshingProvider is newProvider plus a RefreshAfter policy, and the fake
// clock and cache behind it exposed -- the clock so a test can advance past the
// refresh interval, the cache so a test can seed entries at a specific FetchedAt
// directly rather than only through Album's own writes.
func newRefreshingProvider(
	t *testing.T, refreshAfter time.Duration, sources ...musicbrainz.Source,
) (musicbrainz.Provider, *clock.Fake, metacache.Cache) {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	cache := newTestCache(t)
	provider := musicbrainz.New(musicbrainz.Config{
		Sources: sources, Cache: cache, Clock: fake,
		Logger: slog.New(slog.DiscardHandler), RefreshAfter: refreshAfter,
	})
	return provider, fake, cache
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

// Only every source answering 404 says an id is retired; one unreachable source leaves it open.
func TestProvider_NotFoundOnlyWhenEverySourceAnswers404(t *testing.T) {
	gone := func(name string) error {
		return fmt.Errorf("%s: %w", name, &httpx.StatusError{Provider: name, Status: http.StatusNotFound})
	}
	cases := []struct {
		name    string
		direct  error
		retired bool
	}{
		{"every source 404", gone("direct"), true},
		{"one source down", &httpx.StatusError{Provider: "direct", Status: http.StatusServiceUnavailable}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := &fakeSource{name: "proxy", err: gone("proxy")}
			direct := &fakeSource{name: "direct", err: tc.direct}
			_, _, err := newProvider(t, proxy, direct).Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "")
			if err == nil || errors.Is(err, musicbrainz.ErrNotFound) != tc.retired {
				t.Errorf("err = %v, want ErrNotFound %v", err, tc.retired)
			}
		})
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

// A lookup still inside RefreshAfter is served from cache, same as the permanent
// cache's own test above -- naming RefreshAfter explicitly so the policy itself,
// not just the accidental default, is under test.
func TestProvider_ALookupWithinRefreshAfterIsServedFromCache(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1", Title: "KI Pecezinu"}}
	provider, fake, _ := newRefreshingProvider(t, 90*24*time.Hour, direct)

	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("first Album: %v", err)
	}
	fake.Advance(89 * 24 * time.Hour)
	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("second Album: %v", err)
	}
	if direct.calls != 1 {
		t.Errorf("source called %d times, want exactly 1 -- the entry is not stale yet", direct.calls)
	}
}

// Once an entry has aged past RefreshAfter, a lookup must treat it as a miss:
// a real source call, and the cache updated with a new FetchedAt.
func TestProvider_ALookupPastRefreshAfterRefetchesAndUpdatesFetchedAt(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1", Title: "KI Pecezinu"}}
	provider, fake, _ := newRefreshingProvider(t, 90*24*time.Hour, direct)

	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("first Album: %v", err)
	}
	fake.Advance(90 * 24 * time.Hour)
	direct.rg.Title = "KI Pecezinu (Remastered)"
	rg, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1")
	if err != nil {
		t.Fatalf("second Album: %v", err)
	}
	if direct.calls != 2 {
		t.Errorf("source called %d times, want 2 -- the stale entry must be re-fetched", direct.calls)
	}
	if rg.Title != "KI Pecezinu (Remastered)" {
		t.Errorf("Title = %q, want the fresh fetch's answer, not the stale cached one", rg.Title)
	}
}

// RefreshAfter's zero value must mean "never expire" -- the pre-existing
// permanent-cache behaviour every caller that does not set the field relies on.
func TestProvider_ZeroRefreshAfterNeverExpires(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1", Title: "KI Pecezinu"}}
	provider, fake, _ := newRefreshingProvider(t, 0, direct)

	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("first Album: %v", err)
	}
	fake.Advance(100 * 365 * 24 * time.Hour)
	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("second Album: %v", err)
	}
	if direct.calls != 1 {
		t.Errorf("source called %d times, want exactly 1 -- RefreshAfter: 0 must never expire", direct.calls)
	}
}

func TestProvider_SearchSkipsSourcesThatDoNotImplementSearcher(t *testing.T) {
	proxy := &fakeSource{name: "proxy"} // no Searcher
	direct := &fakeSearchSource{fakeSource: fakeSource{name: "direct"},
		matches: []musicbrainz.ReleaseGroupMatch{{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1"}}}}
	provider := newProvider(t, proxy, direct)

	matches, err := provider.Search(t.Context(), musicbrainz.PriorityIdentify, "Vutu Doci", "The Sezo Siluko", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 || matches[0].MBID != "rg1" {
		t.Errorf("matches = %+v, want the one Searcher-capable source's answer", matches)
	}
	if direct.searchCalls != 1 {
		t.Errorf("searchCalls = %d, want 1", direct.searchCalls)
	}
}

// A provider with no Searcher-capable source at all must answer "nothing found",
// not an error -- Search is an optional capability, and a caller with no source
// configured for it should behave exactly like a search that found nothing.
func TestProvider_SearchWithNoSearcherCapableSourceFindsNothing(t *testing.T) {
	proxy := &fakeSource{name: "proxy"}
	provider := newProvider(t, proxy)

	matches, err := provider.Search(t.Context(), musicbrainz.PriorityIdentify, "Vutu Doci", "The Sezo Siluko", 5)
	if err != nil {
		t.Fatalf("Search: %v, want no error when nothing can search at all", err)
	}
	if matches != nil {
		t.Errorf("matches = %+v, want nil", matches)
	}
}

func TestProvider_SearchErrorsWhenEverySearcherFails(t *testing.T) {
	direct := &fakeSearchSource{fakeSource: fakeSource{name: "direct"}, searchErr: errors.New("search down")}
	provider := newProvider(t, direct)

	if _, err := provider.Search(t.Context(), musicbrainz.PriorityIdentify, "Vutu Doci", "The Sezo Siluko", 5); err == nil {
		t.Fatal("Search succeeded although the only Searcher-capable source failed")
	}
}

func TestProvider_SearchByQuerySkipsSourcesThatDoNotImplementQuerySearcher(t *testing.T) {
	proxy := &fakeSource{name: "proxy"} // no QuerySearcher
	direct := &fakeQuerySearchSource{fakeSource: fakeSource{name: "direct"},
		matches: []musicbrainz.ReleaseGroupMatch{{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1"}}}}
	provider := newProvider(t, proxy, direct)

	matches, err := provider.SearchByQuery(t.Context(), musicbrainz.PriorityInteractive, "vutu doci kick inside", 5)
	if err != nil {
		t.Fatalf("SearchByQuery: %v", err)
	}
	if len(matches) != 1 || matches[0].MBID != "rg1" {
		t.Errorf("matches = %+v, want the one QuerySearcher-capable source's answer", matches)
	}
	if direct.searchCalls != 1 {
		t.Errorf("searchCalls = %d, want 1", direct.searchCalls)
	}
}

// A provider with no QuerySearcher-capable source at all must answer "nothing
// found", not an error -- the same optional-capability reasoning Search's own
// test gives.
func TestProvider_SearchByQueryWithNoCapableSourceFindsNothing(t *testing.T) {
	proxy := &fakeSource{name: "proxy"}
	provider := newProvider(t, proxy)

	matches, err := provider.SearchByQuery(t.Context(), musicbrainz.PriorityInteractive, "vutu doci", 5)
	if err != nil {
		t.Fatalf("SearchByQuery: %v, want no error when nothing can search at all", err)
	}
	if matches != nil {
		t.Errorf("matches = %+v, want nil", matches)
	}
}

func TestProvider_SearchByQueryErrorsWhenEveryQuerySearcherFails(t *testing.T) {
	direct := &fakeQuerySearchSource{fakeSource: fakeSource{name: "direct"}, searchErr: errors.New("search down")}
	provider := newProvider(t, direct)

	if _, err := provider.SearchByQuery(t.Context(), musicbrainz.PriorityInteractive, "vutu doci", 5); err == nil {
		t.Fatal("SearchByQuery succeeded although the only QuerySearcher-capable source failed")
	}
}

// A query search and a fielded search must never collide in the cache even
// when they would otherwise hash to related strings -- queryCacheKey's own
// "q\x1f" prefix is what keeps them apart.
func TestProvider_QuerySearchIsServedFromCacheAndNeverCollidesWithAFieldedSearch(t *testing.T) {
	direct := &fakeQuerySearchSource{fakeSource: fakeSource{name: "direct"},
		matches: []musicbrainz.ReleaseGroupMatch{{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1"}}}}
	provider := newProvider(t, direct)

	if _, err := provider.SearchByQuery(t.Context(), musicbrainz.PriorityInteractive, "Vutu Doci", 5); err != nil {
		t.Fatalf("first SearchByQuery: %v", err)
	}
	if _, err := provider.SearchByQuery(t.Context(), musicbrainz.PriorityInteractive, "  vutu doci  ", 5); err != nil {
		t.Fatalf("second SearchByQuery: %v", err)
	}
	if direct.searchCalls != 1 {
		t.Errorf("searchCalls = %d, want 1 -- the second, differently-cased/spaced query must hit the cache", direct.searchCalls)
	}
}

// A second search for the same artist/title, differently cased and spaced, must
// still hit the cache -- the query itself is case-insensitive, the cache should
// not be stricter than the thing it caches.
func TestProvider_ASecondSearchIsServedFromCache(t *testing.T) {
	direct := &fakeSearchSource{fakeSource: fakeSource{name: "direct"},
		matches: []musicbrainz.ReleaseGroupMatch{{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "The Sezo Siluko"}}}}
	provider := newProvider(t, direct)

	if _, err := provider.Search(t.Context(), musicbrainz.PriorityIdentify, "Vutu Doci", "The Sezo Siluko", 5); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	matches, err := provider.Search(t.Context(), musicbrainz.PriorityIdentify, "  vutu doci  ", "the sezo siluko", 5)
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if direct.searchCalls != 1 {
		t.Errorf("searchCalls = %d, want 1 -- the second search should hit the cache", direct.searchCalls)
	}
	if len(matches) != 1 || matches[0].MBID != "rg1" {
		t.Errorf("matches = %+v, want the first search's cached result", matches)
	}
}

func TestProvider_ArtistSkipsSourcesThatDoNotImplementArtistLookup(t *testing.T) {
	proxy := &fakeSource{name: "proxy"} // no ArtistLookup
	direct := &fakeArtistLookupSource{fakeSource: fakeSource{name: "direct"},
		artist: musicbrainz.Artist{MBID: "artist-1", Name: "Vutu Doci"}}
	provider := newProvider(t, proxy, direct)

	artist, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "artist-1")
	if err != nil {
		t.Fatalf("Artist: %v", err)
	}
	if artist.Name != "Vutu Doci" {
		t.Errorf("Artist = %+v, want the one ArtistLookup-capable source's answer", artist)
	}
	if direct.artistCalls != 1 {
		t.Errorf("artistCalls = %d, want 1", direct.artistCalls)
	}
}

func TestProvider_ArtistErrorsWhenNoSourceCanLookUpAnArtist(t *testing.T) {
	proxy := &fakeSource{name: "proxy"}
	provider := newProvider(t, proxy)

	if _, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "artist-1"); err == nil {
		t.Fatal("Artist succeeded with no ArtistLookup-capable source configured")
	}
}

func TestProvider_ArtistErrorsWhenEveryLookupFails(t *testing.T) {
	direct := &fakeArtistLookupSource{fakeSource: fakeSource{name: "direct"}, artistErr: errors.New("lookup down")}
	provider := newProvider(t, direct)

	if _, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "artist-1"); err == nil {
		t.Fatal("Artist succeeded although the only ArtistLookup-capable source failed")
	}
}

func TestProvider_ASecondArtistLookupIsServedFromCache(t *testing.T) {
	direct := &fakeArtistLookupSource{fakeSource: fakeSource{name: "direct"},
		artist: musicbrainz.Artist{MBID: "artist-1", Name: "Vutu Doci"}}
	provider := newProvider(t, direct)

	if _, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "artist-1"); err != nil {
		t.Fatalf("first Artist: %v", err)
	}
	artist, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "artist-1")
	if err != nil {
		t.Fatalf("second Artist: %v", err)
	}
	if direct.artistCalls != 1 {
		t.Errorf("artistCalls = %d, want 1 -- the second lookup should hit the cache", direct.artistCalls)
	}
	if artist.Name != "Vutu Doci" {
		t.Errorf("Artist = %+v, want the first lookup's cached result", artist)
	}
}

func TestProvider_ArtistReleaseGroupsSkipsSourcesThatDoNotImplementDiscography(t *testing.T) {
	proxy := &fakeSource{name: "proxy"} // no Discography
	direct := &fakeDiscographySource{fakeSource: fakeSource{name: "direct"},
		groups: []musicbrainz.ReleaseGroup{{MBID: "rg-1", Title: "KI Pecezinu"}}}
	provider := newProvider(t, proxy, direct)

	groups, err := provider.ArtistReleaseGroups(t.Context(), musicbrainz.PriorityBackfill, "artist-1")
	if err != nil {
		t.Fatalf("ArtistReleaseGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].Title != "KI Pecezinu" {
		t.Errorf("groups = %+v, want the one Discography-capable source's answer", groups)
	}
	if direct.discographyCalls != 1 {
		t.Errorf("discographyCalls = %d, want 1", direct.discographyCalls)
	}
}

func TestProvider_ArtistReleaseGroupsErrorsWhenNoSourceCanBrowse(t *testing.T) {
	proxy := &fakeSource{name: "proxy"}
	provider := newProvider(t, proxy)

	if _, err := provider.ArtistReleaseGroups(t.Context(), musicbrainz.PriorityBackfill, "artist-1"); err == nil {
		t.Fatal("ArtistReleaseGroups succeeded with no Discography-capable source configured")
	}
}

func TestProvider_ASecondArtistReleaseGroupsLookupIsServedFromCache(t *testing.T) {
	direct := &fakeDiscographySource{fakeSource: fakeSource{name: "direct"},
		groups: []musicbrainz.ReleaseGroup{{MBID: "rg-1", Title: "KI Pecezinu"}}}
	provider := newProvider(t, direct)

	if _, err := provider.ArtistReleaseGroups(t.Context(), musicbrainz.PriorityBackfill, "artist-1"); err != nil {
		t.Fatalf("first ArtistReleaseGroups: %v", err)
	}
	groups, err := provider.ArtistReleaseGroups(t.Context(), musicbrainz.PriorityBackfill, "artist-1")
	if err != nil {
		t.Fatalf("second ArtistReleaseGroups: %v", err)
	}
	if direct.discographyCalls != 1 {
		t.Errorf("discographyCalls = %d, want 1 -- the second lookup should hit the cache", direct.discographyCalls)
	}
	if len(groups) != 1 || groups[0].Title != "KI Pecezinu" {
		t.Errorf("groups = %+v, want the first lookup's cached result", groups)
	}
}

// The release-group half and the release half share one all-or-nothing decision,
// consistent with the existing missing-entry case above (a missing entry on
// either side already forces a full re-fetch, no partial optimisation): a stale
// entry on either side must do the same, even when the other side is still
// fresh. Partial re-fetch is out of scope -- an accepted inefficiency. Ages are
// seeded directly through the cache (rather than through two Album calls, which
// would always leave both entries the same age) to get a genuinely asymmetric
// starting point: the release-group entry 90 days old (stale), the release entry
// only 1 day old (fresh).
func TestProvider_OneStaleEntryForcesAFullRefetchOfBoth(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu (Remastered)"},
		rel: musicbrainz.Release{MBID: "rel1", Title: "KI Pecezinu (Remastered)"}}
	provider, fake, cache := newRefreshingProvider(t, 90*24*time.Hour, direct)

	if err := cache.Set(t.Context(), "musicbrainz:release-group:v2", "rg1",
		metacache.Entry{Source: "direct", Body: `{"MBID":"rg1","Title":"KI Pecezinu"}`, FetchedAt: fake.Now()}); err != nil {
		t.Fatalf("seed release-group entry: %v", err)
	}
	fake.Advance(89 * 24 * time.Hour)
	if err := cache.Set(t.Context(), "musicbrainz:release", "rel1",
		metacache.Entry{Source: "direct", Body: `{"MBID":"rel1","Title":"KI Pecezinu"}`, FetchedAt: fake.Now()}); err != nil {
		t.Fatalf("seed release entry: %v", err)
	}
	fake.Advance(24 * time.Hour)
	// Now: release-group entry is 90 days old (stale), release entry is 1 day old (fresh).

	rg, release, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	if direct.calls != 1 {
		t.Errorf("source called %d times, want 1 -- a stale release-group entry must force"+
			" a full re-fetch even though the release entry alone is still fresh", direct.calls)
	}
	if rg.Title != "KI Pecezinu (Remastered)" || release.Title != "KI Pecezinu (Remastered)" {
		t.Errorf("rg=%+v release=%+v, want both entities replaced by the re-fetch, not the"+
			" fresh release entry surviving on its own", rg, release)
	}
}

type fakeReleaseListerSource struct {
	fakeSource
	releases     []musicbrainz.Release
	releasesErr  error
	releaseCalls int
}

func (f *fakeReleaseListerSource) Releases(context.Context, string) ([]musicbrainz.Release, error) {
	f.releaseCalls++
	return f.releases, f.releasesErr
}

// Album identification lists releases for every candidate, so a repeat must not reach the network.
func TestProvider_ASecondReleasesListingIsServedFromCache(t *testing.T) {
	direct := &fakeReleaseListerSource{fakeSource: fakeSource{name: "direct"},
		releases: []musicbrainz.Release{{MBID: "release-1", Title: "KI Pecezinu"}}}
	provider := newProvider(t, direct)

	for range 2 {
		releases, err := provider.Releases(t.Context(), musicbrainz.PriorityIdentify, "rg-1")
		if err != nil {
			t.Fatalf("Releases: %v", err)
		}
		if len(releases) != 1 || releases[0].MBID != "release-1" {
			t.Fatalf("releases = %+v, want the source's one release", releases)
		}
	}
	if direct.releaseCalls != 1 {
		t.Errorf("releaseCalls = %d, want 1 (the second listing from cache)", direct.releaseCalls)
	}
}

type fakeRecordingSearchSource struct {
	fakeSource
	matches     []musicbrainz.RecordingMatch
	searchCalls int
}

func (f *fakeRecordingSearchSource) SearchRecordingReleaseGroups(context.Context, string, string, int) ([]musicbrainz.RecordingMatch, error) {
	f.searchCalls++
	return f.matches, nil
}

func TestProvider_SearchRecordingsSkipsSourcesWithoutItAndCaches(t *testing.T) {
	proxy := &fakeSource{name: "proxy"}
	direct := &fakeRecordingSearchSource{fakeSource: fakeSource{name: "direct"},
		matches: []musicbrainz.RecordingMatch{{RecordingMBID: "recording-1", Title: "Sesoge"}}}
	provider := newProvider(t, proxy, direct)

	for range 2 {
		matches, err := provider.SearchRecordings(t.Context(), musicbrainz.PriorityIdentify, "Katupoken", "Sesoge", 5)
		if err != nil {
			t.Fatalf("SearchRecordings: %v", err)
		}
		if len(matches) != 1 || matches[0].RecordingMBID != "recording-1" {
			t.Fatalf("matches = %+v, want the one capable source's answer", matches)
		}
	}
	if direct.searchCalls != 1 {
		t.Errorf("searchCalls = %d, want 1 (the second search from cache)", direct.searchCalls)
	}
}

// priorityRecordingSource records the request priority each lookup's context carried.
type priorityRecordingSource struct {
	fakeSource
	seen []httpx.Priority
}

func (f *priorityRecordingSource) Album(ctx context.Context, _, _ string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	f.seen = append(f.seen, httpx.PriorityFrom(ctx))
	return musicbrainz.ReleaseGroup{MBID: "rg-1"}, musicbrainz.Release{MBID: "release-1"}, nil
}

// The limiter can only put a user ahead of a scan if the caller's priority reaches the request.
func TestProvider_TheCallersPriorityReachesTheRequest(t *testing.T) {
	source := &priorityRecordingSource{fakeSource: fakeSource{name: "direct"}}
	provider := newProvider(t, source)

	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityInteractive, "rg-1", "release-1"); err != nil {
		t.Fatalf("Album: %v", err)
	}
	if want := []httpx.Priority{httpx.Priority(musicbrainz.PriorityInteractive)}; !slices.Equal(source.seen, want) {
		t.Errorf("priorities seen = %v, want %v", source.seen, want)
	}
}

// fakeArtistSearchSource is a fakeSource that also implements ArtistSearcher.
type fakeArtistSearchSource struct {
	fakeSource
	artistMatches []musicbrainz.ArtistMatch
	searchErr     error
	searchCalls   int
}

func (f *fakeArtistSearchSource) SearchArtists(context.Context, string, int) ([]musicbrainz.ArtistMatch, error) {
	f.searchCalls++
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.artistMatches, nil
}

func TestProvider_SearchArtistsSkipsSourcesThatCannotSearchArtists(t *testing.T) {
	proxy := &fakeSource{name: "proxy"} // no ArtistSearcher
	direct := &fakeArtistSearchSource{
		fakeSource: fakeSource{name: "direct"},
		artistMatches: []musicbrainz.ArtistMatch{
			{Artist: musicbrainz.Artist{MBID: "artist-1", Name: "Vutu Doci"}, Score: 100},
		},
	}
	provider := newProvider(t, proxy, direct)

	matches, err := provider.SearchArtists(t.Context(), musicbrainz.PriorityInteractive, "vutu doci", 10)
	if err != nil {
		t.Fatalf("SearchArtists: %v", err)
	}
	if len(matches) != 1 || matches[0].Name != "Vutu Doci" {
		t.Fatalf("matches = %+v, want the one capable source's answer", matches)
	}
	if direct.searchCalls != 1 {
		t.Errorf("searchCalls = %d, want 1", direct.searchCalls)
	}
}

// A failing source falls through to the next, the same as every other
// capability on this provider.
func TestProvider_SearchArtistsFallsThroughAFailingSource(t *testing.T) {
	first := &fakeArtistSearchSource{fakeSource: fakeSource{name: "first"}, searchErr: errors.New("first down")}
	second := &fakeArtistSearchSource{
		fakeSource: fakeSource{name: "second"},
		artistMatches: []musicbrainz.ArtistMatch{
			{Artist: musicbrainz.Artist{MBID: "artist-2", Name: "Heka"}},
		},
	}
	provider := newProvider(t, first, second)

	matches, err := provider.SearchArtists(t.Context(), musicbrainz.PriorityInteractive, "heka", 10)
	if err != nil {
		t.Fatalf("SearchArtists: %v", err)
	}
	if len(matches) != 1 || matches[0].Name != "Heka" {
		t.Errorf("matches = %+v, want the second source's answer", matches)
	}
}

// No source able to search artists at all is a wiring gap, not a failed
// lookup: the caller cannot act on the difference, so it reads as no results.
func TestProvider_SearchArtistsAnswersNothingWhenNoSourceCanSearch(t *testing.T) {
	provider := newProvider(t, &fakeSource{name: "proxy"})

	matches, err := provider.SearchArtists(t.Context(), musicbrainz.PriorityInteractive, "anyone", 10)
	if err != nil {
		t.Fatalf("SearchArtists: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("matches = %+v, want none", matches)
	}
}

// newOrderedProvider is newProvider with a live order the test can change
// between calls, standing in for someone reordering the list in the interface.
func newOrderedProvider(t *testing.T, order *[]string, sources ...musicbrainz.Source) musicbrainz.Provider {
	t.Helper()
	return musicbrainz.New(musicbrainz.Config{
		Sources: sources, LiveOrder: func(int64) []string { return *order },
		Cache: newTestCache(t), Clock: clock.NewFakeAtEpoch(),
		Logger: slog.New(slog.DiscardHandler),
	})
}

// The order is consulted per lookup, not captured: the jobs hold one Provider
// for the life of the process, so a captured order would need a restart.
func TestProvider_LiveOrderIsReadOnEveryLookup(t *testing.T) {
	first := &fakeSource{name: "first", rg: musicbrainz.ReleaseGroup{MBID: "from-first"}}
	second := &fakeSource{name: "second", rg: musicbrainz.ReleaseGroup{MBID: "from-second"}}
	order := []string{"first", "second"}
	provider := newOrderedProvider(t, &order, first, second)

	group, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "", "a")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	if group.MBID != "from-first" {
		t.Fatalf("MBID = %q, want the first source's answer", group.MBID)
	}

	order = []string{"second", "first"}
	group, _, err = provider.Album(t.Context(), musicbrainz.PriorityEnrich, "", "b")
	if err != nil {
		t.Fatalf("Album after reorder: %v", err)
	}
	if group.MBID != "from-second" {
		t.Errorf("MBID = %q, want the reordered first source's answer", group.MBID)
	}
}

// A source the order leaves out is not consulted at all.
func TestProvider_LiveOrderOmitsUnlistedSources(t *testing.T) {
	skipped := &fakeSource{name: "skipped", rg: musicbrainz.ReleaseGroup{MBID: "wrong"}}
	wanted := &fakeSource{name: "wanted", rg: musicbrainz.ReleaseGroup{MBID: "right"}}
	order := []string{"wanted"}
	provider := newOrderedProvider(t, &order, skipped, wanted)

	group, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "", "a")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	if group.MBID != "right" {
		t.Errorf("MBID = %q, want the listed source", group.MBID)
	}
	if skipped.calls != 0 {
		t.Errorf("skipped source was called %d times, want 0", skipped.calls)
	}
}

// Direct MusicBrainz answers even when an order forgot it: something has to
// answer, and the others are proxies of it (ADR-29).
// Direct MusicBrainz is asked only where the order names it: switched off, it is left out (user, 2026-10-23).
func TestProvider_AnOrderWithoutDirectMusicBrainzNeverAsksIt(t *testing.T) {
	direct := &fakeSource{name: musicbrainz.DirectName, rg: musicbrainz.ReleaseGroup{MBID: "from-direct"}}
	order := []string{"something-else"}
	provider := newOrderedProvider(t, &order, direct)

	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "", "a"); err == nil || direct.calls != 0 {
		t.Errorf("Album = %v after %d direct calls; want no source to answer and direct never asked", err, direct.calls)
	}
}

// "Refresh metadata" asks the source again even with a fresh cache entry, and caches the new answer.
func TestProvider_WithFreshSkipsTheCacheAndRewritesIt(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1", Title: "KI Pecezinu"}}
	provider := newProvider(t, direct)
	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("first Album: %v", err)
	}

	direct.rg.Title = "KI Pecezinu OKNOTOK"
	rg, _, err := provider.Album(musicbrainz.WithFresh(t.Context()), musicbrainz.PriorityRefresh, "rg1", "rel1")
	if err != nil || rg.Title != "KI Pecezinu OKNOTOK" || direct.calls != 2 {
		t.Fatalf("fresh Album = %q, %v after %d calls; want the source's new title from a second call", rg.Title, err, direct.calls)
	}
	rg, _, _ = provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1")
	if rg.Title != "KI Pecezinu OKNOTOK" || direct.calls != 2 {
		t.Errorf("later cached Album = %q after %d calls; want the refreshed answer from the cache", rg.Title, direct.calls)
	}
}

func TestProvider_AnOutageServesTheStoredAnswerPastRefreshAfter(t *testing.T) {
	direct := &fakeSource{name: "direct", rg: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "KI Pecezinu"},
		rel: musicbrainz.Release{MBID: "rel1", Title: "KI Pecezinu"}}
	provider, fake, _ := newRefreshingProvider(t, 90*24*time.Hour, direct)
	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); err != nil {
		t.Fatalf("first Album: %v", err)
	}
	fake.Advance(91 * 24 * time.Hour)
	direct.err = errors.New("musicbrainz down")

	rg, release, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1")
	if err != nil || rg.Title != "KI Pecezinu" || release.MBID != "rel1" {
		t.Errorf("Album during an outage = %+v, %+v, %v; want the stored answer", rg, release, err)
	}
	if direct.calls != 2 {
		t.Errorf("source called %d times, want 2 -- the stale entry is tried against the source first", direct.calls)
	}
	if _, _, err := provider.Album(musicbrainz.WithFresh(t.Context()), musicbrainz.PriorityEnrich, "rg1", "rel1"); err == nil {
		t.Error("a refresh a person asked for was answered from the stored copy")
	}
}

func TestProvider_AnOutageServesAStoredArtistButAMissingOneStaysAnError(t *testing.T) {
	direct := &fakeArtistLookupSource{fakeSource: fakeSource{name: "direct"}, artist: musicbrainz.Artist{MBID: "a1", Name: "Vutu Doci"}}
	provider, fake, _ := newRefreshingProvider(t, 90*24*time.Hour, direct)
	if _, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "a1"); err != nil {
		t.Fatalf("first Artist: %v", err)
	}
	fake.Advance(91 * 24 * time.Hour)
	direct.artistErr = errors.New("musicbrainz down")
	if artist, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "a1"); err != nil || artist.Name != "Vutu Doci" {
		t.Errorf("Artist during an outage = %+v, %v", artist, err)
	}
	if _, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "a2"); err == nil {
		t.Error("an artist never stored was answered during an outage")
	}
}

func TestProvider_AnIdEverySourceAnswered404IsNotAskedAgainForADay(t *testing.T) {
	gone := &httpx.StatusError{Status: http.StatusNotFound}
	direct := &fakeSource{name: "direct", err: gone}
	provider, fake, _ := newRefreshingProvider(t, 90*24*time.Hour, direct)
	for range 2 {
		if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); !errors.Is(err, musicbrainz.ErrNotFound) {
			t.Fatalf("Album err = %v, want ErrNotFound", err)
		}
	}
	if direct.calls != 1 {
		t.Errorf("source called %d times, want 1 within the day", direct.calls)
	}
	fake.Advance(25 * time.Hour)
	if _, _, err := provider.Album(t.Context(), musicbrainz.PriorityEnrich, "rg1", "rel1"); !errors.Is(err, musicbrainz.ErrNotFound) || direct.calls != 2 {
		t.Errorf("after a day: err = %v, calls = %d, want ErrNotFound and 2", err, direct.calls)
	}
}

type fakeReleaseGroupSource struct {
	fakeSource
	group     string
	groupErr  error
	groupAsks int
}

func (f *fakeReleaseGroupSource) ReleaseGroupOf(context.Context, string) (string, error) {
	f.groupAsks++
	return f.group, f.groupErr
}

// The release-to-group answer falls through a failing source and is asked once.
func TestProvider_ReleaseGroupOfFallsThroughAndCaches(t *testing.T) {
	proxy := &fakeReleaseGroupSource{fakeSource: fakeSource{name: "proxy"}, groupErr: errors.New("down")}
	direct := &fakeReleaseGroupSource{fakeSource: fakeSource{name: "direct"}, group: "rg-1"}
	provider := newProvider(t, proxy, direct)

	for range 2 {
		group, err := provider.ReleaseGroupOf(t.Context(), musicbrainz.PriorityIdentify, "release-1")
		if err != nil || group != "rg-1" {
			t.Fatalf("ReleaseGroupOf = %q, %v; want rg-1", group, err)
		}
	}
	if proxy.groupAsks != 1 || direct.groupAsks != 1 {
		t.Errorf("asks = proxy %d, direct %d; want 1 each (the second from cache)", proxy.groupAsks, direct.groupAsks)
	}
}

// Down everywhere is not "no such release": only the latter may let an import drop the id.
func TestProvider_ReleaseGroupOfOutageIsNotNotFound(t *testing.T) {
	down := &fakeReleaseGroupSource{fakeSource: fakeSource{name: "direct"}, groupErr: errors.New("down")}
	_, err := newProvider(t, down).ReleaseGroupOf(t.Context(), musicbrainz.PriorityIdentify, "release-1")
	if err == nil || errors.Is(err, musicbrainz.ErrNotFound) {
		t.Errorf("ReleaseGroupOf = %v, want an outage error that is not ErrNotFound", err)
	}
}

// META-6: an artist, a discography or a release list every source answered 404 for is not asked again within the day.
func TestProvider_AnArtistADiscographyAndAReleaseListThatAre404AreNotAskedAgain(t *testing.T) {
	gone := &httpx.StatusError{Status: http.StatusNotFound}
	artist := &fakeArtistLookupSource{fakeSource: fakeSource{name: "artist"}, artistErr: gone}
	discography := &fakeDiscographySource{fakeSource: fakeSource{name: "discography"}, discographyErr: gone}
	lister := &fakeReleaseListerSource{fakeSource: fakeSource{name: "lister"}, releasesErr: gone}
	provider := newProvider(t, artist, discography, lister)
	for range 2 {
		if _, err := provider.Artist(t.Context(), musicbrainz.PriorityEnrich, "a1"); !errors.Is(err, musicbrainz.ErrNotFound) {
			t.Fatalf("Artist err = %v, want ErrNotFound", err)
		}
		if _, err := provider.ArtistReleaseGroups(t.Context(), musicbrainz.PriorityEnrich, "a1"); !errors.Is(err, musicbrainz.ErrNotFound) {
			t.Fatalf("ArtistReleaseGroups err = %v, want ErrNotFound", err)
		}
		if _, err := provider.Releases(t.Context(), musicbrainz.PriorityEnrich, "rg1"); !errors.Is(err, musicbrainz.ErrNotFound) {
			t.Fatalf("Releases err = %v, want ErrNotFound", err)
		}
	}
	if artist.artistCalls != 1 || discography.discographyCalls != 1 || lister.releaseCalls != 1 {
		t.Errorf("calls = %d, %d, %d; want 1 each", artist.artistCalls, discography.discographyCalls, lister.releaseCalls)
	}
}

func TestProvider_DiscographyFollowsTheLibrarysBootlegSwitchAndCachesEachAnswerApart(t *testing.T) {
	direct := &fakeDiscographySource{fakeSource: fakeSource{name: "direct"},
		groups: []musicbrainz.ReleaseGroup{{MBID: "rg-1", Title: "KI Pecezinu"}}}
	included := map[int64]bool{1: false, 2: true}
	provider := musicbrainz.New(musicbrainz.Config{
		Sources: []musicbrainz.Source{direct}, LiveBootlegs: func(library int64) bool { return included[library] },
		Cache: newTestCache(t), Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	})
	for _, library := range []int64{1, 2, 1, 2} {
		if _, err := provider.ArtistReleaseGroups(musicbrainz.ForLibrary(t.Context(), library), musicbrainz.PriorityBackfill, "artist-1"); err != nil {
			t.Fatalf("ArtistReleaseGroups library %d: %v", library, err)
		}
	}
	if want := []bool{false, true}; !slices.Equal(direct.sawBootlegOnly, want) {
		t.Errorf("source saw bootleg-only = %v, want %v: one fetch per switch position, the repeats served from the cache", direct.sawBootlegOnly, want)
	}
}
