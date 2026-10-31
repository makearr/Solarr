package settings_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestALibraryStartsWithTheProvidersOfItsTypeAndMayReorderThem(t *testing.T) {
	cache, _ := newCache(t)
	movies, err := cache.CreateCategory(t.Context(), "movie", "Movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cache.Snapshot()
	created, _ := snapshot.Category(movies.ID)
	if !slices.Equal(snapshot.MetadataSourcesFor(created), []string{"wikidata", "tmdb", "radarr", "mdblist", "omdb", "tvdb", "fanart"}) {
		t.Fatalf("a new movie library starts with %v", snapshot.MetadataSourcesFor(created))
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), movies.ID, []string{"radarr", "tmdb"}); err != nil {
		t.Fatal(err)
	}
	reordered, _ := cache.Snapshot().Category(movies.ID)
	if !slices.Equal(reordered.MetadataSources, []string{"radarr", "tmdb"}) {
		t.Errorf("sources = %v", reordered.MetadataSources)
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), movies.ID, []string{"tvmaze"}); err == nil {
		t.Error("a TV provider is no source for a movie library")
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), movies.ID, []string{"tmdb", "tmdb"}); err == nil {
		t.Error("a provider is named once")
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), movies.ID, nil); err != nil {
		t.Fatal(err)
	}
	snapshot = cache.Snapshot()
	again, _ := snapshot.Category(movies.ID)
	if !slices.Equal(snapshot.MetadataSourcesFor(again), settings.DefaultMovieSources) {
		t.Errorf("an emptied order follows the default: %v", snapshot.MetadataSourcesFor(again))
	}
}

func TestAProviderSwitchedOffIsAskedByNoLibrary(t *testing.T) {
	cache, fake := newCache(t)
	movies, err := cache.CreateCategory(t.Context(), "movie", "Movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	for key, enabled := range map[string]bool{"radarr": false, "wikidata": false} {
		if err := cache.SetProviderEnabled(t.Context(), fake, key, enabled); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := cache.Snapshot()
	category, _ := snapshot.Category(movies.ID)
	if got := snapshot.MetadataSourcesFor(category); !slices.Equal(got, []string{"tmdb", "mdblist", "omdb", "tvdb", "fanart"}) {
		t.Errorf("sources = %v; want what is still switched on", got)
	}
	if *snapshot.MetadataSettings().Wikidata {
		t.Error("Wikidata switched off is off for artist photos too")
	}
	if err := cache.SetProviderEnabled(t.Context(), fake, "radarr", true); err != nil {
		t.Fatal(err)
	}
	if !cache.Snapshot().ProviderEnabled("radarr") {
		t.Error("switched on again")
	}
	if err := cache.SetProviderEnabled(t.Context(), fake, "nonsense", false); err == nil {
		t.Error("an unknown provider is refused")
	}
}

// Direct MusicBrainz can be switched off (user, 2026-10-23), and then is no fallback; the last music source on stays on.
func TestMusicBrainzCanBeSwitchedOffButNotTheLastMusicSource(t *testing.T) {
	cache, fake := newCache(t)
	if got := cache.Snapshot().MusicSourceOrder(0); !slices.Equal(got, []string{"brainzmash", "musicbrainz-proxy", "musicbrainz"}) {
		t.Fatalf("starting order = %v", got)
	}
	if err := cache.SetProviderEnabled(t.Context(), fake, "musicbrainz", false); err != nil {
		t.Fatalf("switching MusicBrainz off: %v", err)
	}
	if got := cache.Snapshot().MusicSourceOrder(0); !slices.Equal(got, []string{"brainzmash", "musicbrainz-proxy"}) {
		t.Errorf("order with MusicBrainz off = %v, want it gone, fallback too", got)
	}
	if err := cache.SetProviderEnabled(t.Context(), fake, "brainzmash", false); err != nil {
		t.Fatalf("switching BrainzMash off: %v", err)
	}
	if err := cache.SetProviderEnabled(t.Context(), fake, "musicbrainz-proxy", false); err == nil {
		t.Error("the last music source was switched off")
	}
	if err := cache.SetProviderEnabled(t.Context(), fake, "musicbrainz", true); err != nil {
		t.Fatalf("switching MusicBrainz on: %v", err)
	}
	if got := cache.Snapshot().MusicSourceOrder(0); !slices.Equal(got, []string{"musicbrainz-proxy", "musicbrainz"}) {
		t.Errorf("order with MusicBrainz back = %v", got)
	}
}

// Each music library is looked up in its own order; a lookup for no music library takes the first one's.
func TestEachMusicLibraryIsLookedUpInItsOwnOrder(t *testing.T) {
	cache, _ := newCache(t)
	first, err := cache.CreateCategory(t.Context(), "music", "Music", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.CreateCategory(t.Context(), "music", "Classical", 1)
	if err != nil {
		t.Fatal(err)
	}
	movies, err := cache.CreateCategory(t.Context(), "movie", "Movies", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), second.ID, []string{"musicbrainz-proxy", "brainzmash"}); err != nil {
		t.Fatal(err)
	}
	snapshot := cache.Snapshot()
	shipped := []string{"brainzmash", "musicbrainz-proxy", "musicbrainz"}
	for _, check := range []struct {
		name     string
		category int64
		want     []string
	}{
		{"no library", 0, shipped},
		{"the first music library", first.ID, shipped},
		{"the second music library", second.ID, []string{"musicbrainz-proxy", "brainzmash", "musicbrainz"}},
		{"a movie library", movies.ID, shipped},
		{"an unknown library", 999, shipped},
	} {
		if got := snapshot.MusicSourceOrder(check.category); !slices.Equal(got, check.want) {
			t.Errorf("%s: order = %v, want %v", check.name, got, check.want)
		}
	}
}

// A pace a person sets replaces the shipped one, up to what the provider allows; 0 returns to the shipped pace.
func TestProviderPaceIsAPersonsUpToTheProvidersLimit(t *testing.T) {
	cache, fake := newCache(t)
	if pace, own := cache.Snapshot().ProviderPace("tmdb"); pace != 2400 || own {
		t.Fatalf("tmdb = %d (own %v), want the shipped 2400", pace, own)
	}
	if err := cache.SetProviderPace(t.Context(), fake, "tmdb", 600); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetProviderPace(t.Context(), fake, "musicbrainz", 30); err != nil {
		t.Fatal(err)
	}
	if pace, own := cache.Snapshot().ProviderPace("tmdb"); pace != 600 || !own {
		t.Errorf("tmdb = %d (own %v), want 600 of the person's", pace, own)
	}
	if err := cache.SetProviderPace(t.Context(), fake, "musicbrainz", 61); err == nil {
		t.Error("MusicBrainz faster than its one a second was accepted")
	}
	if err := cache.SetProviderPace(t.Context(), fake, "tmdb", 0); err != nil {
		t.Fatal(err)
	}
	if pace, own := cache.Snapshot().ProviderPace("tmdb"); pace != 2400 || own {
		t.Errorf("tmdb after 0 = %d (own %v), want the shipped pace", pace, own)
	}
	if pace, _ := cache.Snapshot().ProviderPace("musicbrainz"); pace != 30 {
		t.Errorf("musicbrainz = %d, want its own 30 kept", pace)
	}
}

func TestAnAdultLibraryAsksWikidataWhisparrAndTheStashBoxServersInThatOrder(t *testing.T) {
	cache, _ := newCache(t)
	adult, err := cache.CreateCategory(t.Context(), "adult", "Adult", 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cache.Snapshot()
	created, _ := snapshot.Category(adult.ID)
	if want := []string{"wikidata", "whisparr", "stashdb", "theporndb", "fansdb"}; !slices.Equal(snapshot.MetadataSourcesFor(created), want) {
		t.Errorf("a new adult library starts with %v, want %v", snapshot.MetadataSourcesFor(created), want)
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), adult.ID, []string{"fansdb", "stashdb"}); err != nil {
		t.Errorf("a stash-box server alone is a valid order: %v", err)
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), adult.ID, []string{"tmdb"}); err == nil {
		t.Error("a film provider is no source for an adult library")
	}
	for _, key := range []string{"stashdb", "theporndb", "fansdb"} {
		if _, has := settings.ProviderKeySetting(key); !has {
			t.Errorf("%s takes an owner's key", key)
		}
	}
}

// Merging a library attaches its root folders to the target, drops its access grants and removes it; another media type
// is refused and changes nothing.
func TestMergeCategory_MovesTheFoldersAndRemovesTheLibrary(t *testing.T) {
	cache, _ := newCache(t)
	ctx := t.Context()
	target, err := cache.CreateCategory(ctx, "audiobook", "Audiobooks", 0)
	if err != nil {
		t.Fatal(err)
	}
	source, err := cache.CreateCategory(ctx, "audiobook", "Audible", 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := cache.CreateCategory(ctx, "movie", "Movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := cache.CreateRootFolder(ctx, "/audible", "Audible")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.AttachRootFolder(ctx, settings.CategoryRoot{CategoryID: source.ID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true}); err != nil {
		t.Fatal(err)
	}
	if err := cache.MergeCategory(ctx, source.ID, other.ID); err == nil {
		t.Error("a library was merged into another media type's")
	}
	if err := cache.MergeCategory(ctx, source.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	snapshot := cache.Snapshot()
	if _, found := snapshot.Category(source.ID); found {
		t.Error("the merged library still exists")
	}
	if roots := snapshot.CategoriesForRootFolder(root.ID); len(roots) != 1 || roots[0] != target.ID {
		t.Errorf("the folder is attached to %v, want only the target", roots)
	}
}
