package musicbrainz_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/metadata/metacache"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
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
}

func (f *fakeDiscographySource) ArtistReleaseGroups(context.Context, string) ([]musicbrainz.ReleaseGroup, error) {
	f.discographyCalls++
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

	if err := cache.Set(t.Context(), "musicbrainz:release-group", "rg1",
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
	releaseCalls int
}

func (f *fakeReleaseListerSource) Releases(context.Context, string) ([]musicbrainz.Release, error) {
	f.releaseCalls++
	return f.releases, nil
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
