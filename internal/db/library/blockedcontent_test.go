package library_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestBlockedContent_CreateRequiresAtLeastOneHash(t *testing.T) {
	opened, _ := newLibrary(t)
	blocks := library.NewBlockedContentStore(opened)

	_, err := blocks.Create(t.Context(), library.NewBlock{
		SizeBytes: 5000, Reason: library.BlockReasonFake, Source: library.BlockSourceUser,
		CreatedAt: 1,
	})
	if !errors.Is(err, library.ErrNoHash) {
		t.Fatalf("Create with no hash = %v, want ErrNoHash", err)
	}
}

func TestBlockedContent_CreateAndGet(t *testing.T) {
	opened, _ := newLibrary(t)
	blocks := library.NewBlockedContentStore(opened)

	created, err := blocks.Create(t.Context(), library.NewBlock{
		OSHash: "abc123", SizeBytes: 5000, Reason: library.BlockReasonFake,
		Note: "not the real album", Title: "Fake Album", Source: library.BlockSourceUser,
		CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.HitCount != 0 {
		t.Errorf("HitCount = %d, want 0", created.HitCount)
	}
	if created.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil (permanent)", created.ExpiresAt)
	}

	got, err := blocks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != created {
		t.Errorf("Get = %+v, want %+v", got, created)
	}
}

func TestBlockedContent_FindByHashMatchesEitherHash(t *testing.T) {
	opened, _ := newLibrary(t)
	blocks := library.NewBlockedContentStore(opened)

	byOSHash, err := blocks.Create(t.Context(), library.NewBlock{
		OSHash: "oshash-1", SizeBytes: 100, Reason: library.BlockReasonFake,
		Source: library.BlockSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create oshash: %v", err)
	}
	byQuickHash, err := blocks.Create(t.Context(), library.NewBlock{
		QuickHash: "quickhash-1", SizeBytes: 100, Reason: library.BlockReasonFake,
		Source: library.BlockSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create quick_hash: %v", err)
	}

	found, err := blocks.FindByHash(t.Context(), "oshash-1", "", 100)
	if err != nil {
		t.Fatalf("FindByHash oshash: %v", err)
	}
	if len(found) != 1 || found[0].ID != byOSHash.ID {
		t.Errorf("FindByHash(oshash-1) = %+v, want just %d", found, byOSHash.ID)
	}

	found, err = blocks.FindByHash(t.Context(), "", "quickhash-1", 100)
	if err != nil {
		t.Fatalf("FindByHash quick_hash: %v", err)
	}
	if len(found) != 1 || found[0].ID != byQuickHash.ID {
		t.Errorf("FindByHash(quickhash-1) = %+v, want just %d", found, byQuickHash.ID)
	}

	found, err = blocks.FindByHash(t.Context(), "no-such-hash", "no-such-hash-either", 100)
	if err != nil {
		t.Fatalf("FindByHash no match: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("FindByHash(no match) = %+v, want empty", found)
	}
}

// Invariant 83 / library health's own "blocked hashes found in the library"
// figure (todo 4.8): a block matches a media_object already registered
// through an ordinary path, not only one an import gate refused.
func TestBlockedContent_ListPresentInLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	blocks := library.NewBlockedContentStore(opened)

	present := createObject(t, opened, "present-quickhash", 100)
	write(t, opened, func(ctx context.Context, queries *librarydb.Queries) error {
		_, err := queries.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: present.ID, RootFolderID: 7, RelPath: "Fake Artist/Fake Album/01.flac",
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
		})
		return err
	})
	_ = createObject(t, opened, "absent-quickhash", 100) // never blocked, must not appear

	blockedRow, err := blocks.Create(t.Context(), library.NewBlock{
		QuickHash: "present-quickhash", SizeBytes: 100, Reason: library.BlockReasonFake,
		Title: "Fake Album", Source: library.BlockSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := blocks.ListPresentInLibrary(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("ListPresentInLibrary: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("ListPresentInLibrary = %+v, want exactly 1", found)
	}
	if found[0].BlockedContentID != blockedRow.ID || found[0].MediaObjectID != present.ID ||
		found[0].RootFolderID != 7 || found[0].RelPath != "Fake Artist/Fake Album/01.flac" ||
		found[0].Reason != library.BlockReasonFake {
		t.Errorf("found = %+v, want it naming block %d and object %d", found, blockedRow.ID, present.ID)
	}
}

func TestBlockedContent_FindByHashIgnoresExpiredEntries(t *testing.T) {
	opened, _ := newLibrary(t)
	blocks := library.NewBlockedContentStore(opened)

	expiresAt := int64(50)
	_, err := blocks.Create(t.Context(), library.NewBlock{
		OSHash: "auto-block", SizeBytes: 100, Reason: library.BlockReasonUnwanted,
		Source: library.BlockSourceAuto, CreatedAt: 1, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := blocks.FindByHash(t.Context(), "auto-block", "", 10)
	if err != nil {
		t.Fatalf("FindByHash before expiry: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("FindByHash before expiry = %+v, want 1 match", found)
	}

	found, err = blocks.FindByHash(t.Context(), "auto-block", "", 100)
	if err != nil {
		t.Fatalf("FindByHash after expiry: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("FindByHash after expiry = %+v, want no match", found)
	}
}

func TestBlockedContent_RecordHitAndRemove(t *testing.T) {
	opened, _ := newLibrary(t)
	blocks := library.NewBlockedContentStore(opened)

	created, err := blocks.Create(t.Context(), library.NewBlock{
		OSHash: "abc123", SizeBytes: 100, Reason: library.BlockReasonFake,
		Source: library.BlockSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := blocks.RecordHit(t.Context(), created.ID, 42); err != nil {
		t.Fatalf("RecordHit: %v", err)
	}
	got, err := blocks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HitCount != 1 || got.LastHitAt == nil || *got.LastHitAt != 42 {
		t.Errorf("after RecordHit: HitCount=%d LastHitAt=%v, want 1 and 42", got.HitCount, got.LastHitAt)
	}

	if err := blocks.Remove(t.Context(), created.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := blocks.Get(t.Context(), created.ID); err == nil {
		t.Error("row still exists after Remove")
	}
}

func TestBlockedContent_PurgeExpiredLeavesPermanentBlocksAlone(t *testing.T) {
	opened, _ := newLibrary(t)
	blocks := library.NewBlockedContentStore(opened)

	expiresAt := int64(50)
	expired, err := blocks.Create(t.Context(), library.NewBlock{
		OSHash: "auto-block", SizeBytes: 100, Reason: library.BlockReasonUnwanted,
		Source: library.BlockSourceAuto, CreatedAt: 1, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("Create expiring: %v", err)
	}
	permanent, err := blocks.Create(t.Context(), library.NewBlock{
		OSHash: "manual-block", SizeBytes: 100, Reason: library.BlockReasonFake,
		Source: library.BlockSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create permanent: %v", err)
	}

	if err := blocks.PurgeExpired(t.Context(), 100); err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if _, err := blocks.Get(t.Context(), expired.ID); err == nil {
		t.Error("expired block still exists after PurgeExpired")
	}
	if _, err := blocks.Get(t.Context(), permanent.ID); err != nil {
		t.Error("permanent block was purged")
	}
}
