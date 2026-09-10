package settings_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/migrations"
)

// newCacheWithBackupDir is newCache plus access to the store and backup directory,
// which EnableBackupOnChange's tests need to inspect and newCache does not expose.
func newCacheWithBackupDir(t *testing.T) (*settings.Cache, *store.Store, string) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return cache, opened, backups
}

// rotationBackups counts only BackupRotation entries: Migrate's own pre-migration
// backup (Invariant 7) is always present and would otherwise throw every count here
// off by one.
func rotationBackups(t *testing.T, opened *store.Store, dir string) int {
	t.Helper()
	found, err := opened.ListBackups(dir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	count := 0
	for _, backup := range found {
		if backup.Kind == store.BackupRotation {
			count++
		}
	}
	return count
}

func TestBackupOnChange_DisabledByDefault(t *testing.T) {
	cache, opened, backups := newCacheWithBackupDir(t)

	if _, err := cache.CreateCategory(t.Context(), "music", "Music", 100); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if count := rotationBackups(t, opened, backups); count != 0 {
		t.Errorf("rotation backups = %d, want 0 -- EnableBackupOnChange was never called", count)
	}
}

func TestBackupOnChange_BacksUpAfterAWrite(t *testing.T) {
	cache, opened, backups := newCacheWithBackupDir(t)
	cache.EnableBackupOnChange(backups, 5, nil)

	if _, err := cache.CreateCategory(t.Context(), "music", "Music", 100); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if count := rotationBackups(t, opened, backups); count != 1 {
		t.Errorf("rotation backups = %d, want 1", count)
	}
}

// Four writes with keep=3 must leave exactly 3 rotation backups: not 4 (rotation
// never ran) and not fewer (the enforced minimum, retention.go, would explain 3 even
// if pruning used the wrong keep, but not a number under that).
func TestBackupOnChange_PrunesToKeep(t *testing.T) {
	cache, opened, backups := newCacheWithBackupDir(t)
	cache.EnableBackupOnChange(backups, 3, nil)

	for index := range 4 {
		name := fmt.Sprintf("Category %d", index)
		if _, err := cache.CreateCategory(t.Context(), "music", name, 100); err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
	}

	if count := rotationBackups(t, opened, backups); count != 3 {
		t.Errorf("rotation backups = %d, want 3", count)
	}
}

// TouchAPIKey runs on every authenticated request and must never trigger a backup --
// unlike CreateCategory above, it bypasses Cache's write/Reload path entirely because
// API keys are not part of the Snapshot.
func TestBackupOnChange_APIKeyTouchNeverBacksUp(t *testing.T) {
	cache, opened, backups := newCacheWithBackupDir(t)
	cache.EnableBackupOnChange(backups, 5, nil)
	fake := clock.NewFakeAtEpoch()

	user, err := cache.CreateUser(t.Context(), fake, "admin", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// CreateUser itself is a write and legitimately backs up; only what follows
	// matters here.
	before := rotationBackups(t, opened, backups)

	key, err := cache.CreateAPIKey(t.Context(), fake, user.ID, "test", "hash")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if err := cache.TouchAPIKey(t.Context(), fake, key.ID); err != nil {
		t.Fatalf("TouchAPIKey: %v", err)
	}

	after := rotationBackups(t, opened, backups)
	if after != before {
		t.Errorf("rotation backups went from %d to %d after CreateAPIKey/TouchAPIKey, want unchanged",
			before, after)
	}
}
