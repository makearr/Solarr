package adapters

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// gigabyte makes the sizes readable beside the default 8 GB reserve every root keeps.
const gigabyte = 1_000_000_000

// twoRootLibrary is a library with two roots that both take new files: root A on filesystem "fs-a" with 20 GB free,
// root B on "fs-b" with bigger free space (bFree).
func twoRootLibrary(t *testing.T, bFree int64, bOnline bool) (*settings.Snapshot, settings.Category, [2]int64) {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "backups"), 0o750); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(directory, "backups"), owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatal(err)
	}
	category, err := cache.CreateCategory(t.Context(), "music", "Target", 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids [2]int64
	for index, spec := range []struct {
		path, fsID string
		free       int64
	}{{"/a", "fs-a", 20 * gigabyte}, {"/b", "fs-b", bFree}} {
		root, err := cache.CreateRootFolder(t.Context(), spec.path, spec.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{CategoryID: category.ID, RootFolderID: root.ID, AcceptsNew: true}); err != nil {
			t.Fatal(err)
		}
		if err := cache.SetRootFolderCapacity(t.Context(), root.ID, settings.RootFolderCapacity{
			Writable: true, TotalBytes: 100 * gigabyte, FreeBytes: spec.free, CheckedAt: 1, FSID: spec.fsID,
		}); err != nil {
			t.Fatal(err)
		}
		ids[index] = root.ID
	}
	for index, id := range ids {
		if err := cache.SetRootFolderOnline(t.Context(), id, index == 0 || bOnline); err != nil {
			t.Fatalf("SetRootFolderOnline: %v", err)
		}
	}
	return cache.Snapshot(), category, ids
}

func TestDestinationRoot_FollowsThePlacementRules(t *testing.T) {
	snapshot, target, ids := twoRootLibrary(t, 20*gigabyte, true)
	move := LibraryMove{}

	// Equal room: the work's own filesystem breaks the tie, so the move can be a rename.
	if got, ok := move.destinationRoot(snapshot, target, []int64{ids[0]}, 0, nil); !ok || got != ids[0] {
		t.Errorf("tie on room = %d, %v; want root A (the work's filesystem)", got, ok)
	}
	// Earlier works of the same request will fill A: the next one goes to B.
	if got, ok := move.destinationRoot(snapshot, target, []int64{ids[0]}, 0, map[int64]int64{ids[0]: 15 * gigabyte}); !ok || got != ids[1] {
		t.Errorf("after 15 GB planned on A = %d, %v; want root B", got, ok)
	}
}

func TestDestinationRoot_SkipsAnOfflineRoot(t *testing.T) {
	snapshot, target, ids := twoRootLibrary(t, 50*gigabyte, false)
	if got, ok := (LibraryMove{}).destinationRoot(snapshot, target, nil, 0, nil); !ok || got != ids[0] {
		t.Errorf("destination = %d, %v; want the online root A", got, ok)
	}
}

func TestDestinationRoot_SkipsARootTheWorkDoesNotFitOn(t *testing.T) {
	snapshot, target, ids := twoRootLibrary(t, 50*gigabyte, true)
	if got, ok := (LibraryMove{}).destinationRoot(snapshot, target, []int64{ids[0]}, 30*gigabyte, nil); !ok || got != ids[1] {
		t.Errorf("a 30 GB work with 20 GB free on its own filesystem = %d, %v; want root B", got, ok)
	}
}

func TestWorkBytes_AddsTheFilesOfAWorkAndOfItsParts(t *testing.T) {
	detail := library.WorkDetail{
		Files: []library.PartFile{{SizeBytes: 5}},
		Parts: []library.PartDetail{{File: &library.PartFile{SizeBytes: 7}}, {}},
	}
	if got := workBytes(detail); got != 12 {
		t.Errorf("workBytes = %d, want 12", got)
	}
}
