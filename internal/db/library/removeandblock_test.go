package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func addPath(t *testing.T, opened *store.Store, objectID, rootFolderID int64, relPath string) int64 {
	t.Helper()
	var id int64
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		var err error
		id, err = q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: objectID, RootFolderID: rootFolderID, RelPath: relPath,
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1, LastSeenAt: 1,
		})
		return err
	})
	return id
}

func rootPathLookup(paths map[int64]string) library.RootFolderPath {
	return func(rootFolderID int64) (string, bool) {
		p, ok := paths[rootFolderID]
		return p, ok
	}
}

func TestRemoveAndBlock_FullActionOnARealDesiredItem(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)

	putTestFile(t, fsys, "/media/root/Artist/Album/track.flac", []byte("fake audio"))

	work := createWork(t, opened, "music", "Fake Album")
	item := createDesired(t, opened, work.ID, "SET", 1)
	object := createObject(t, opened, "quick-1", 4096)
	addPath(t, opened, object.ID, 1, "Artist/Album/track.flac")
	attach(t, opened, item.ID, object.ID, "primary", "disc1:track1")
	if err := library.NewDesired(opened).RecomputeAlbum(t.Context(), item.ID, work.ID); err != nil {
		t.Fatalf("RecomputeAlbum (test setup): %v", err)
	}

	before, err := librarydb.New(opened.Read()).GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem before: %v", err)
	}
	if before.PresentCount != 1 {
		t.Fatalf("PresentCount before = %d, want 1 (test setup)", before.PresentCount)
	}

	action := library.NewRemoveAndBlock(
		library.NewRecycleBin(opened, fsys, fake),
		library.NewBlockedContentStore(opened),
		library.NewBlocklistStore(opened),
		library.NewDesired(opened),
		library.NewPhysical(opened),
		rootPathLookup(map[int64]string{1: "/media/root"}),
	)

	itemID := item.ID
	result, err := action.Do(t.Context(), library.RemoveAndBlockRequest{
		MediaObjectID: object.ID,
		RecycleBin:    true, RecycleReason: library.ReasonJunk, RecycleRetain: 168 * time.Hour,
		Block: true, OSHash: "oshash-1", BlockReason: library.BlockReasonFake, BlockTitle: "Fake Album",
		Blocklist: true, ContentHash: "content-hash-1", BlocklistTitle: "Fake Album", DesiredItemID: &itemID,
		Reopen: true,
		At:     100,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	if len(result.RecycledPaths) != 1 {
		t.Fatalf("RecycledPaths = %+v, want 1 entry", result.RecycledPaths)
	}
	if _, err := fsys.Stat("/media/root/Artist/Album/track.flac"); err == nil {
		t.Error("original file still exists after Do")
	}
	if _, err := fsys.Stat(result.RecycledPaths[0].RecycledPath); err != nil {
		t.Errorf("recycle bin path does not exist: %v", err)
	}

	if result.Block == nil || result.Block.OSHash != "oshash-1" {
		t.Errorf("Block = %+v, want an oshash-1 block", result.Block)
	}
	if result.Blocklisted == nil || result.Blocklisted.ContentHash != "content-hash-1" {
		t.Errorf("Blocklisted = %+v, want a content-hash-1 entry", result.Blocklisted)
	}
	if len(result.ReopenedItems) != 1 || result.ReopenedItems[0] != item.ID {
		t.Errorf("ReopenedItems = %v, want [%d]", result.ReopenedItems, item.ID)
	}

	after, err := librarydb.New(opened.Read()).GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem after: %v", err)
	}
	if after.PresentCount != 0 {
		t.Errorf("PresentCount after = %d, want 0 -- the item must return to wanting the file", after.PresentCount)
	}
	if after.State != "MISSING" {
		t.Errorf("State after = %q, want MISSING", after.State)
	}

	if _, err := librarydb.New(opened.Read()).GetMediaObject(t.Context(), object.ID); err == nil {
		t.Error("media_object still exists after Do -- it should be deleted once every copy is recycled")
	}
}

func TestRemoveAndBlock_RecyclesEveryHardlinkedCopy(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)

	putTestFile(t, fsys, "/media/root-a/track.flac", []byte("fake audio"))
	putTestFile(t, fsys, "/media/root-b/track-copy.flac", []byte("fake audio"))

	object := createObject(t, opened, "quick-1", 4096)
	addPath(t, opened, object.ID, 1, "track.flac")
	addPath(t, opened, object.ID, 2, "track-copy.flac")

	action := library.NewRemoveAndBlock(
		library.NewRecycleBin(opened, fsys, fake),
		library.NewBlockedContentStore(opened),
		library.NewBlocklistStore(opened),
		library.NewDesired(opened),
		library.NewPhysical(opened),
		rootPathLookup(map[int64]string{1: "/media/root-a", 2: "/media/root-b"}),
	)

	result, err := action.Do(t.Context(), library.RemoveAndBlockRequest{
		MediaObjectID: object.ID, RecycleBin: true, RecycleReason: library.ReasonJunk,
		RecycleRetain: time.Hour, At: 1,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(result.RecycledPaths) != 2 {
		t.Fatalf("RecycledPaths = %+v, want both copies recycled", result.RecycledPaths)
	}
	if _, err := fsys.Stat("/media/root-a/track.flac"); err == nil {
		t.Error("copy in root-a still exists")
	}
	if _, err := fsys.Stat("/media/root-b/track-copy.flac"); err == nil {
		t.Error("copy in root-b still exists")
	}
}

func TestRemoveAndBlock_CrossDeviceCopiesAreRecycled(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)

	putTestFile(t, fsys, "/media/root-a/track.flac", []byte("fake audio"))
	putTestFile(t, fsys, "/media/root-b/track-copy.flac", []byte("fake audio"))

	object := createObject(t, opened, "quick-1", 4096)
	addPath(t, opened, object.ID, 1, "track.flac")
	addPath(t, opened, object.ID, 2, "track-copy.flac")

	recycleBin := library.NewRecycleBin(opened, fsys, fake)
	action := library.NewRemoveAndBlock(
		recycleBin, library.NewBlockedContentStore(opened), library.NewBlocklistStore(opened),
		library.NewDesired(opened), library.NewPhysical(opened),
		rootPathLookup(map[int64]string{1: "/media/root-a", 2: "/media/root-b"}),
	)

	fsys.SetRenameSupported(false)

	result, err := action.Do(t.Context(), library.RemoveAndBlockRequest{
		MediaObjectID: object.ID, RecycleBin: true, RecycleReason: library.ReasonJunk,
		RecycleRetain: time.Hour, At: 1,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(result.RecycledPaths) != 2 {
		t.Errorf("RecycledPaths = %+v, want both copies recycled by copy and verify", result.RecycledPaths)
	}
	for _, entry := range result.RecycledPaths {
		if _, err := fsys.Stat(entry.RecycledPath); err != nil {
			t.Errorf("recycled copy %q missing: %v", entry.RecycledPath, err)
		}
	}
	if _, err := fsys.Stat("/media/root-a/track.flac"); err == nil {
		t.Error("root-a copy still exists")
	}
	if _, err := fsys.Stat("/media/root-b/track-copy.flac"); err == nil {
		t.Error("root-b copy still exists")
	}
}

func TestRemoveAndBlock_ReopenWithoutRecycleBinStillDetaches(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)

	work := createWork(t, opened, "music", "Real Album")
	item := createDesired(t, opened, work.ID, "SET", 1)
	object := createObject(t, opened, "quick-1", 4096)
	attach(t, opened, item.ID, object.ID, "primary", "disc1:track1")

	action := library.NewRemoveAndBlock(
		library.NewRecycleBin(opened, fsys, fake), library.NewBlockedContentStore(opened),
		library.NewBlocklistStore(opened), library.NewDesired(opened), library.NewPhysical(opened),
		rootPathLookup(nil),
	)

	result, err := action.Do(t.Context(), library.RemoveAndBlockRequest{
		MediaObjectID: object.ID, Reopen: true, At: 1,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(result.ReopenedItems) != 1 || result.ReopenedItems[0] != item.ID {
		t.Errorf("ReopenedItems = %v, want [%d]", result.ReopenedItems, item.ID)
	}

	after, err := librarydb.New(opened.Read()).GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if after.PresentCount != 0 {
		t.Errorf("PresentCount = %d, want 0", after.PresentCount)
	}

	// The object itself was never recycled or deleted -- Recycle bin was
	// not requested.
	if _, err := librarydb.New(opened.Read()).GetMediaObject(t.Context(), object.ID); err != nil {
		t.Error("media_object should still exist -- Recycle bin was not requested")
	}
}

func TestRemoveAndBlock_OnlyBlockRunsWhenOnlyBlockIsRequested(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)

	object := createObject(t, opened, "quick-1", 4096)

	action := library.NewRemoveAndBlock(
		library.NewRecycleBin(opened, fsys, fake), library.NewBlockedContentStore(opened),
		library.NewBlocklistStore(opened), library.NewDesired(opened), library.NewPhysical(opened),
		rootPathLookup(nil),
	)

	result, err := action.Do(t.Context(), library.RemoveAndBlockRequest{
		MediaObjectID: object.ID, Block: true, OSHash: "oshash-only", BlockReason: library.BlockReasonUnwanted,
		At: 1,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if result.Block == nil {
		t.Fatal("Block result is nil")
	}
	if result.Blocklisted != nil || len(result.RecycledPaths) != 0 || len(result.ReopenedItems) != 0 {
		t.Errorf("result = %+v, want only Block set", result)
	}
	// Untouched: no recycle bin and no delete requested.
	if _, err := librarydb.New(opened.Read()).GetMediaObject(t.Context(), object.ID); err != nil {
		t.Error("media_object should still exist -- Recycle bin was not requested")
	}
}

func TestRemoveAndBlock_MissingRootPathIsAnError(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)

	object := createObject(t, opened, "quick-1", 4096)
	addPath(t, opened, object.ID, 999, "track.flac")

	action := library.NewRemoveAndBlock(
		library.NewRecycleBin(opened, fsys, fake), library.NewBlockedContentStore(opened),
		library.NewBlocklistStore(opened), library.NewDesired(opened), library.NewPhysical(opened),
		rootPathLookup(nil), // no root folder known at all
	)

	_, err := action.Do(t.Context(), library.RemoveAndBlockRequest{
		MediaObjectID: object.ID, RecycleBin: true, RecycleReason: library.ReasonJunk,
		RecycleRetain: time.Hour, At: 1,
	})
	if err == nil {
		t.Fatal("Do succeeded, want it refused: no known path for root folder 999")
	}
}
