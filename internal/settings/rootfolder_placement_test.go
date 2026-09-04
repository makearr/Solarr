package settings_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
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
