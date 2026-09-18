package library_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestBlocklist_CreateRequiresGuidOrContentHash(t *testing.T) {
	opened, _ := newLibrary(t)
	blocklist := library.NewBlocklistStore(opened)

	_, err := blocklist.Create(t.Context(), library.NewBlocklistEntry{
		Title: "Some Release", Reason: library.BlocklistManual,
		Source: library.BlocklistSourceUser, CreatedAt: 1,
	})
	if !errors.Is(err, library.ErrNoBlocklistKey) {
		t.Fatalf("Create with no key = %v, want ErrNoBlocklistKey", err)
	}
}

func TestBlocklist_CreateAndIsBlockedByGuid(t *testing.T) {
	opened, _ := newLibrary(t)
	blocklist := library.NewBlocklistStore(opened)

	indexerID := int64(7)
	_, err := blocklist.Create(t.Context(), library.NewBlocklistEntry{
		IndexerID: &indexerID, GUID: "release-guid-1", Title: "Fake Release",
		Reason: library.BlocklistManual, Source: library.BlocklistSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	blocked, err := blocklist.IsBlocked(t.Context(), &indexerID, "release-guid-1", "", 100)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Error("IsBlocked(matching guid) = false, want true")
	}

	other := int64(8)
	blocked, err = blocklist.IsBlocked(t.Context(), &other, "release-guid-1", "", 100)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if blocked {
		t.Error("IsBlocked(different indexer, same guid) = true, want false -- guid is scoped per indexer")
	}
}

func TestBlocklist_CreateAndIsBlockedByContentHash(t *testing.T) {
	opened, _ := newLibrary(t)
	blocklist := library.NewBlocklistStore(opened)

	_, err := blocklist.Create(t.Context(), library.NewBlocklistEntry{
		ContentHash: "content-hash-1", Title: "Fake Release",
		Reason: library.BlocklistManual, Source: library.BlocklistSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	blocked, err := blocklist.IsBlocked(t.Context(), nil, "", "content-hash-1", 100)
	if err != nil {
		t.Fatalf("IsBlocked: %v", err)
	}
	if !blocked {
		t.Error("IsBlocked(matching content hash) = false, want true")
	}
}

func TestBlocklist_IsBlockedIgnoresExpiredEntries(t *testing.T) {
	opened, _ := newLibrary(t)
	blocklist := library.NewBlocklistStore(opened)

	expiresAt := int64(50)
	_, err := blocklist.Create(t.Context(), library.NewBlocklistEntry{
		ContentHash: "content-hash-1", Title: "Stalled Release",
		Reason: library.BlocklistStalled, Source: library.BlocklistSourceAuto,
		CreatedAt: 1, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	blocked, err := blocklist.IsBlocked(t.Context(), nil, "", "content-hash-1", 10)
	if err != nil {
		t.Fatalf("IsBlocked before expiry: %v", err)
	}
	if !blocked {
		t.Error("IsBlocked before expiry = false, want true")
	}

	blocked, err = blocklist.IsBlocked(t.Context(), nil, "", "content-hash-1", 100)
	if err != nil {
		t.Fatalf("IsBlocked after expiry: %v", err)
	}
	if blocked {
		t.Error("IsBlocked after expiry = true, want false")
	}
}

func TestBlocklist_RemoveAndPurgeExpired(t *testing.T) {
	opened, _ := newLibrary(t)
	blocklist := library.NewBlocklistStore(opened)

	manual, err := blocklist.Create(t.Context(), library.NewBlocklistEntry{
		ContentHash: "manual-hash", Title: "Manual Block",
		Reason: library.BlocklistManual, Source: library.BlocklistSourceUser, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create manual: %v", err)
	}
	expiresAt := int64(50)
	if _, err := blocklist.Create(t.Context(), library.NewBlocklistEntry{
		ContentHash: "auto-hash", Title: "Auto Block",
		Reason: library.BlocklistStalled, Source: library.BlocklistSourceAuto,
		CreatedAt: 1, ExpiresAt: &expiresAt,
	}); err != nil {
		t.Fatalf("Create auto: %v", err)
	}

	if err := blocklist.PurgeExpired(t.Context(), 100); err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	entries, err := blocklist.List(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != manual.ID {
		t.Errorf("after PurgeExpired: entries = %+v, want just the manual one (%d)", entries, manual.ID)
	}

	if err := blocklist.Remove(t.Context(), manual.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	entries, err = blocklist.List(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("List after Remove: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries after Remove = %+v, want empty", entries)
	}
}
