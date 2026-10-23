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
	if got := cache.Snapshot().MusicSourceOrder(); !slices.Equal(got, []string{"brainzmash", "musicbrainz-proxy", "musicbrainz"}) {
		t.Fatalf("starting order = %v", got)
	}
	if err := cache.SetProviderEnabled(t.Context(), fake, "musicbrainz", false); err != nil {
		t.Fatalf("switching MusicBrainz off: %v", err)
	}
	if got := cache.Snapshot().MusicSourceOrder(); !slices.Equal(got, []string{"brainzmash", "musicbrainz-proxy"}) {
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
	if got := cache.Snapshot().MusicSourceOrder(); !slices.Equal(got, []string{"musicbrainz-proxy", "musicbrainz"}) {
		t.Errorf("order with MusicBrainz back = %v", got)
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
