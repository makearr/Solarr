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
	if !slices.Equal(snapshot.MetadataSourcesFor(created), []string{"tmdb", "radarr"}) {
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
		t.Errorf("an emptied order follows the instance's: %v", snapshot.MetadataSourcesFor(again))
	}
}
