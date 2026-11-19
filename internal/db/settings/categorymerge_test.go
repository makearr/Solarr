package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

// DB-14: everything a library owns in settings.db follows a merge or blocks a delete, never dangles.
func TestMergeCategory_CarriesWhatTheSourceOwns(t *testing.T) {
	cache, _ := newCache(t)
	ctx := t.Context()
	from, err := cache.CreateCategory(ctx, "music", "From", 100)
	if err != nil {
		t.Fatal(err)
	}
	into, err := cache.CreateCategory(ctx, "music", "Into", 101)
	if err != nil {
		t.Fatal(err)
	}
	list := settings.ImportListRequest{Name: "Favourites", Type: "watchlist", URL: "https://example.invalid/a", CadenceHours: 24, Enabled: true}
	for _, owner := range []int64{from.ID, into.ID} {
		if _, err := cache.CreateImportList(ctx, owner, list); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.CreateDownloadFolder(ctx, settings.DownloadFolderRequest{Path: "/downloads/from", Label: "x", CategoryID: &from.ID, Readable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.CreateExclusionPattern(ctx, settings.ExclusionPattern{Pattern: "*.nfo", Action: settings.ExclusionIgnore, MediaType: "music", CategoryID: &from.ID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	root, err := cache.CreateRootFolder(ctx, t.TempDir(), "merge")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetPlacementAffinity(ctx, from.ID, "key", root.ID, 1); err != nil {
		t.Fatal(err)
	}

	if err := cache.DeleteCategory(ctx, from.ID); !errors.Is(err, settings.ErrInUse) {
		t.Fatalf("delete with owned rows: %v, want in use", err)
	}
	if err := cache.MergeCategory(ctx, from.ID, into.ID); err != nil {
		t.Fatalf("MergeCategory: %v", err)
	}
	lists, err := cache.ImportLists(ctx, into.ID)
	if err != nil || len(lists) != 2 {
		t.Errorf("import lists of the target = %+v, %v; want both, the second renamed", lists, err)
	}
	folders, _ := cache.DownloadFolders(ctx)
	if len(folders) != 1 || folders[0].CategoryID == nil || *folders[0].CategoryID != into.ID {
		t.Errorf("download folders = %+v, want the one moved to the target", folders)
	}
	patterns := 0
	for _, pattern := range cache.Snapshot().ExclusionPatterns() {
		if pattern.CategoryID != nil && *pattern.CategoryID == into.ID {
			patterns++
		}
	}
	if patterns != 1 {
		t.Errorf("patterns of the target = %d, want 1", patterns)
	}
	if got, found, _ := cache.PlacementAffinity(ctx, into.ID, "key"); !found || got != root.ID {
		t.Errorf("placement rule of the target = %d %v", got, found)
	}
}
