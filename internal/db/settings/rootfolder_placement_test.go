package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestCreateRootFolder_DefaultsToWritableAndNotReadOnly(t *testing.T) {
	cache, _ := newCache(t)
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "music-hdd-1")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if !root.Writable {
		t.Error("Writable = false, want true for a freshly created root (probed later, not assumed broken)")
	}
	if root.ReadOnly || root.AllowRename {
		t.Errorf("ReadOnly=%v AllowRename=%v, want both false by default", root.ReadOnly, root.AllowRename)
	}
	if root.TotalBytes != nil || root.FreeBytes != nil || root.CapacityCheckedAt != nil {
		t.Error("capacity fields should be nil until the capacity job has run once")
	}
}

func TestSetRootFolderPolicy_UpdatesReadOnlyAllowRenameAndReserve(t *testing.T) {
	cache, _ := newCache(t)
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	if err := cache.SetRootFolderPolicy(t.Context(), root.ID, true, true, 50_000_000_000); err != nil {
		t.Fatalf("SetRootFolderPolicy: %v", err)
	}

	updated, found := cache.Snapshot().RootFolder(root.ID)
	if !found {
		t.Fatal("RootFolder not found after SetRootFolderPolicy")
	}
	if !updated.ReadOnly || !updated.AllowRename {
		t.Errorf("ReadOnly=%v AllowRename=%v, want both true", updated.ReadOnly, updated.AllowRename)
	}
	if updated.MinFreeBytes != 50_000_000_000 {
		t.Errorf("MinFreeBytes = %d, want 50_000_000_000", updated.MinFreeBytes)
	}
	// Writable must be untouched by a policy write -- only the capacity job
	// ever changes it.
	if !updated.Writable {
		t.Error("Writable changed by SetRootFolderPolicy, want it left alone")
	}
}

func TestSetRootFolderPolicy_RejectsANegativeReserve(t *testing.T) {
	cache, _ := newCache(t)
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := cache.SetRootFolderPolicy(t.Context(), root.ID, false, false, -1); err == nil {
		t.Fatal("SetRootFolderPolicy: expected an error for a negative reserve, got nil")
	}
}

func TestSetRootFolderOnline_TogglesAndPersists(t *testing.T) {
	cache, _ := newCache(t)
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if !root.Online {
		t.Fatal("a freshly created root should default to online")
	}

	if err := cache.SetRootFolderOnline(t.Context(), root.ID, false); err != nil {
		t.Fatalf("SetRootFolderOnline(false): %v", err)
	}
	offline, _ := cache.Snapshot().RootFolder(root.ID)
	if offline.Online {
		t.Error("Online = true after SetRootFolderOnline(false)")
	}

	if err := cache.SetRootFolderOnline(t.Context(), root.ID, true); err != nil {
		t.Fatalf("SetRootFolderOnline(true): %v", err)
	}
	online, _ := cache.Snapshot().RootFolder(root.ID)
	if !online.Online {
		t.Error("Online = false after SetRootFolderOnline(true)")
	}
}

func TestSetRootFolderOnline_RefusesAnUnknownID(t *testing.T) {
	cache, _ := newCache(t)
	if err := cache.SetRootFolderOnline(t.Context(), 999, false); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("SetRootFolderOnline(999) = %v, want ErrNotFound", err)
	}
}

func TestSetRootFolderCapacity_RecordsWhatTheProbeFound(t *testing.T) {
	cache, _ := newCache(t)
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	err = cache.SetRootFolderCapacity(t.Context(), root.ID, settings.RootFolderCapacity{
		Writable: false, WritableReason: "owned by uid 1001, running as uid 1000",
		TotalBytes: 2_000_000_000_000, FreeBytes: 500_000_000_000,
		CheckedAt: 1700000000, FSID: "zfs-pool-a", CapabilitiesJSON: `{"reflink":true}`,
	})
	if err != nil {
		t.Fatalf("SetRootFolderCapacity: %v", err)
	}

	updated, found := cache.Snapshot().RootFolder(root.ID)
	if !found {
		t.Fatal("RootFolder not found after SetRootFolderCapacity")
	}
	if updated.Writable {
		t.Error("Writable = true, want false (the probe found it unwritable)")
	}
	if updated.WritableReason == "" {
		t.Error("WritableReason is empty, want the probe's reason")
	}
	if updated.TotalBytes == nil || *updated.TotalBytes != 2_000_000_000_000 {
		t.Errorf("TotalBytes = %v, want 2_000_000_000_000", updated.TotalBytes)
	}
	if updated.FreeBytes == nil || *updated.FreeBytes != 500_000_000_000 {
		t.Errorf("FreeBytes = %v, want 500_000_000_000", updated.FreeBytes)
	}
	if updated.FSID != "zfs-pool-a" {
		t.Errorf("FSID = %q, want \"zfs-pool-a\"", updated.FSID)
	}
	if updated.CapacityCheckedAt == nil || *updated.CapacityCheckedAt != 1700000000 {
		t.Errorf("CapacityCheckedAt = %v, want 1700000000", updated.CapacityCheckedAt)
	}
}

func TestPlacementAffinity_RoundTrips(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	if _, found, err := cache.PlacementAffinity(t.Context(), category.ID, "artist:42"); err != nil || found {
		t.Fatalf("PlacementAffinity before any write: found=%v err=%v, want false, nil", found, err)
	}

	if err := cache.SetPlacementAffinity(t.Context(), category.ID, "artist:42", root.ID, 1700000000); err != nil {
		t.Fatalf("SetPlacementAffinity: %v", err)
	}

	rootID, found, err := cache.PlacementAffinity(t.Context(), category.ID, "artist:42")
	if err != nil {
		t.Fatalf("PlacementAffinity: %v", err)
	}
	if !found || rootID != root.ID {
		t.Errorf("PlacementAffinity = (%d, %v), want (%d, true)", rootID, found, root.ID)
	}
}

func TestCreateRootFolder_DefaultsToDrainStateNone(t *testing.T) {
	cache, _ := newCache(t)
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if root.DrainState != settings.DrainStateNone || root.DrainJobID != nil {
		t.Errorf("DrainState=%q DrainJobID=%v, want NONE/nil for a freshly created root",
			root.DrainState, root.DrainJobID)
	}
}

func TestSetRootFolderDrainState_RecordsStateAndJobID(t *testing.T) {
	cache, _ := newCache(t)
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	jobID := int64(42)
	if err := cache.SetRootFolderDrainState(t.Context(), root.ID, settings.DrainStateDraining, &jobID); err != nil {
		t.Fatalf("SetRootFolderDrainState: %v", err)
	}
	updated, _ := cache.Snapshot().RootFolder(root.ID)
	if updated.DrainState != settings.DrainStateDraining || updated.DrainJobID == nil || *updated.DrainJobID != jobID {
		t.Errorf("root = %+v, want DRAINING with drain_job_id 42", updated)
	}

	if err := cache.SetRootFolderDrainState(t.Context(), root.ID, settings.DrainStateNone, nil); err != nil {
		t.Fatalf("SetRootFolderDrainState (clear): %v", err)
	}
	cleared, _ := cache.Snapshot().RootFolder(root.ID)
	if cleared.DrainState != settings.DrainStateNone || cleared.DrainJobID != nil {
		t.Errorf("root = %+v, want NONE/nil after clearing", cleared)
	}
}

func TestSetAcceptsNewForRootFolder_FlipsEveryCategoryAttachment(t *testing.T) {
	cache, _ := newCache(t)
	music, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	videos, err := cache.CreateCategory(t.Context(), "music", "Music Videos", 200)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/shared", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	for _, category := range []settings.Category{music, videos} {
		if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
			CategoryID: category.ID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true,
		}); err != nil {
			t.Fatalf("AttachRootFolder(%d): %v", category.ID, err)
		}
	}

	if err := cache.SetAcceptsNewForRootFolder(t.Context(), root.ID, false); err != nil {
		t.Fatalf("SetAcceptsNewForRootFolder: %v", err)
	}
	for _, attachment := range cache.Snapshot().CategoryRoots() {
		if attachment.RootFolderID != root.ID {
			continue
		}
		if attachment.AcceptsNew {
			t.Errorf("category %d still accepts new placements after draining", attachment.CategoryID)
		}
	}
}

func TestCategoriesForRootFolder_ReturnsEveryAttachedCategory(t *testing.T) {
	cache, _ := newCache(t)
	music, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	other, err := cache.CreateRootFolder(t.Context(), "/media/other", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: music.ID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}

	categoryIDs := cache.Snapshot().CategoriesForRootFolder(root.ID)
	if len(categoryIDs) != 1 || categoryIDs[0] != music.ID {
		t.Errorf("CategoriesForRootFolder(root) = %v, want [%d]", categoryIDs, music.ID)
	}
	if got := cache.Snapshot().CategoriesForRootFolder(other.ID); len(got) != 0 {
		t.Errorf("CategoriesForRootFolder(other) = %v, want none", got)
	}
}

func TestPlacementAffinity_UpdateOverwritesThePreviousRoot(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	first, err := cache.CreateRootFolder(t.Context(), "/media/music-1", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	second, err := cache.CreateRootFolder(t.Context(), "/media/music-2", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	if err := cache.SetPlacementAffinity(t.Context(), category.ID, "artist:42", first.ID, 1); err != nil {
		t.Fatalf("SetPlacementAffinity: %v", err)
	}
	if err := cache.SetPlacementAffinity(t.Context(), category.ID, "artist:42", second.ID, 2); err != nil {
		t.Fatalf("SetPlacementAffinity (update): %v", err)
	}

	rootID, found, err := cache.PlacementAffinity(t.Context(), category.ID, "artist:42")
	if err != nil {
		t.Fatalf("PlacementAffinity: %v", err)
	}
	if !found || rootID != second.ID {
		t.Errorf("PlacementAffinity = (%d, %v), want (%d, true)", rootID, found, second.ID)
	}
}
