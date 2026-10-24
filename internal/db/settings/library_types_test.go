package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// A library gains a further media type as a member that keeps the library's name, priority, rating limit and access,
// follows them when they change, and makes the library refuse deletion while it is there (plan 10-libraries L4).
func TestAddLibraryType_KeepsTheMemberInStep(t *testing.T) {
	cache, fake := newCache(t)
	anime, err := cache.CreateCategory(t.Context(), "tv", "Anime", 3)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	thirteen := int64(13)
	if _, err := cache.SetCategoryRatingLimit(t.Context(), anime.ID, &thirteen, false); err != nil {
		t.Fatalf("SetCategoryRatingLimit: %v", err)
	}
	user, err := cache.CreateUser(t.Context(), fake, "housemate", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), user.ID, anime.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}

	movies, err := cache.AddLibraryType(t.Context(), anime.ID, "movie")
	if err != nil {
		t.Fatalf("AddLibraryType: %v", err)
	}
	if movies.LibraryID != anime.ID || movies.Name != "Anime" || movies.PriorityRank != 3 || movies.MaxAge == nil || *movies.MaxAge != 13 {
		t.Errorf("member = %+v, want Anime's name, rank 3 and limit 13 under library %d", movies, anime.ID)
	}
	if got := cache.Snapshot().Permission(user.ID, movies.ID); got != settings.PermissionRead {
		t.Errorf("member access = %s, want the library's read", got)
	}
	var problems config.Problems
	if _, err := cache.AddLibraryType(t.Context(), anime.ID, "tv"); !errors.As(err, &problems) {
		t.Errorf("a second TV member: %v, want problems", err)
	}

	if _, err := cache.UpdateCategory(t.Context(), anime.ID, "Anime and animation", 2, nil, nil, settings.MonitorDiscoveredMonitored); err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if err := cache.SyncLibrary(t.Context(), anime.ID); err != nil {
		t.Fatalf("SyncLibrary: %v", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), user.ID, anime.ID, settings.PermissionManage); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	synced, _ := cache.Snapshot().Category(movies.ID)
	if synced.Name != "Anime and animation" || synced.PriorityRank != 2 || cache.Snapshot().Permission(user.ID, movies.ID) != settings.PermissionManage {
		t.Errorf("after the library changed: member %+v, access %s", synced, cache.Snapshot().Permission(user.ID, movies.ID))
	}
	if got := cache.Snapshot().LibraryCategories(movies.ID); len(got) != 2 || got[0].ID != anime.ID || got[1].ID != movies.ID {
		t.Errorf("library categories = %+v, want Anime then its movies", got)
	}

	var inUse *settings.InUseError
	if err := cache.DeleteCategory(t.Context(), anime.ID); !errors.As(err, &inUse) {
		t.Errorf("deleting a library with a further type: %v, want in use", err)
	}
}
