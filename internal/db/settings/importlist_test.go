package settings_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestCreateImportList_RoundTrips(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	created, err := cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", URL: "https://example.invalid/list",
		CadenceHours: 24, Monitor: true, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateImportList: %v", err)
	}
	if created.ID == 0 || created.CategoryID != category.ID || created.Name != "Favourites" {
		t.Errorf("created = %+v", created)
	}

	lists, err := cache.ImportLists(t.Context(), category.ID)
	if err != nil {
		t.Fatalf("ImportLists: %v", err)
	}
	if len(lists) != 1 || lists[0].ID != created.ID {
		t.Errorf("ImportLists = %+v, want exactly the one created", lists)
	}
}

func TestCreateImportList_RejectsUnknownCategory(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateImportList(t.Context(), 999, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24,
	})
	if !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateImportList_RejectsEmptyName(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	_, err = cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Type: "watchlist", CadenceHours: 24,
	})
	if err == nil {
		t.Fatal("err = nil, want a validation error for an empty name")
	}
}

func TestCreateImportList_RejectsNonPositiveCadence(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	_, err = cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 0,
	})
	if err == nil {
		t.Fatal("err = nil, want a validation error for a non-positive cadence")
	}
}

func TestUpdateImportList_ChangesTheStoredFields(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	created, err := cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateImportList: %v", err)
	}

	updated, err := cache.UpdateImportList(t.Context(), created.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 12, Monitor: true, Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpdateImportList: %v", err)
	}
	if updated.CadenceHours != 12 || updated.Monitor != true || updated.Enabled != false {
		t.Errorf("updated = %+v, want cadence=12 monitor=true enabled=false", updated)
	}
}

func TestUpdateImportList_MissingIDAnswersNotFound(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.UpdateImportList(t.Context(), 999, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24,
	})
	if !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteImportList_RemovesIt(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	created, err := cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24,
	})
	if err != nil {
		t.Fatalf("CreateImportList: %v", err)
	}

	if err := cache.DeleteImportList(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteImportList: %v", err)
	}
	lists, err := cache.ImportLists(t.Context(), category.ID)
	if err != nil {
		t.Fatalf("ImportLists: %v", err)
	}
	if len(lists) != 0 {
		t.Errorf("ImportLists = %+v, want none after delete", lists)
	}
}

// Import lists are scoped per category: one category's lists must not leak
// into another's.
func TestImportLists_ScopedPerCategory(t *testing.T) {
	cache, _ := newCache(t)
	music, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory (music): %v", err)
	}
	tv, err := cache.CreateCategory(t.Context(), "tv", "Television", 100)
	if err != nil {
		t.Fatalf("CreateCategory (tv): %v", err)
	}
	if _, err := cache.CreateImportList(t.Context(), music.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24,
	}); err != nil {
		t.Fatalf("CreateImportList: %v", err)
	}

	lists, err := cache.ImportLists(t.Context(), tv.ID)
	if err != nil {
		t.Fatalf("ImportLists: %v", err)
	}
	if len(lists) != 0 {
		t.Errorf("ImportLists (tv) = %+v, want none: the list belongs to music", lists)
	}
}

func TestEnabledImportLists_AreTheEnabledOnesOfEveryLibraryWithTheirSettings(t *testing.T) {
	cache, _ := newCache(t)
	tv, err := cache.CreateCategory(t.Context(), "tv", "Television", 100)
	if err != nil {
		t.Fatal(err)
	}
	anime, err := cache.CreateCategory(t.Context(), "tv", "Anime", 100)
	if err != nil {
		t.Fatal(err)
	}
	create := func(categoryID int64, name string, enabled bool) settings.ImportList {
		t.Helper()
		created, err := cache.CreateImportList(t.Context(), categoryID, settings.ImportListRequest{
			Name: name, Type: "anilist_season", CadenceHours: 24, Enabled: enabled, Options: `{"season":"next"}`, MonitorOption: "future",
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	first := create(anime.ID, "Next season", true)
	create(tv.ID, "Off", false)
	second := create(tv.ID, "Also on", true)

	enabled, err := cache.EnabledImportLists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(enabled, []settings.ImportList{first, second}) {
		t.Errorf("enabled = %+v, want %+v", enabled, []settings.ImportList{first, second})
	}
	if first.Options != `{"season":"next"}` || first.MonitorOption != "future" {
		t.Errorf("created = %+v: options and monitor option round-trip", first)
	}
	if got, err := cache.ImportList(t.Context(), second.ID); err != nil || !reflect.DeepEqual(got, second) {
		t.Errorf("ImportList = %+v, err = %v", got, err)
	}
	if _, err := cache.ImportList(t.Context(), 999); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("missing: err = %v", err)
	}
}
