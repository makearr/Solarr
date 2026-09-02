package store_test

import (
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/store"
)

// listNames returns just the base names ListBackups found, for readable assertions.
func listNames(t *testing.T, backups []store.BackupInfo) []string {
	t.Helper()
	names := make([]string, len(backups))
	for index, backup := range backups {
		names[index] = backup.Path
	}
	return names
}

func TestListBackups_OrdersNewestFirst(t *testing.T) {
	fixture := newMigrationFixture(t)

	oldest, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	fixture.clock.Advance(time.Hour)
	newest, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	found, err := fixture.store.ListBackups(fixture.backupDir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(found) != 2 || found[0].Path != newest || found[1].Path != oldest {
		t.Errorf("got %v, want [%s %s]", listNames(t, found), newest, oldest)
	}
}

func TestListBackups_ReportsTheKindEachWasTakenAs(t *testing.T) {
	fixture := newMigrationFixture(t)
	if _, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	fixture.clock.Advance(time.Hour)
	if _, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupPreMigration); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	found, err := fixture.store.ListBackups(fixture.backupDir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(found) != 2 || found[0].Kind != store.BackupPreMigration || found[1].Kind != store.BackupRotation {
		t.Fatalf("got %+v, want [premigration rotation]", found)
	}
}

// The retention floor applies even when configuration asks for fewer.
func TestPruneBackups_NeverGoesBelowTheEnforcedMinimum(t *testing.T) {
	fixture := newMigrationFixture(t)
	for range 3 {
		if _, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation); err != nil {
			t.Fatalf("Backup: %v", err)
		}
		fixture.clock.Advance(time.Hour)
	}

	removed, err := fixture.store.PruneBackups(fixture.backupDir, 1)
	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v, want nothing: 3 backups must survive a keep=1 request", removed)
	}
}

func TestPruneBackups_RemovesOnlyTheOldestBeyondKeep(t *testing.T) {
	fixture := newMigrationFixture(t)
	var paths []string
	for range 5 {
		path, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation)
		if err != nil {
			t.Fatalf("Backup: %v", err)
		}
		paths = append(paths, path)
		fixture.clock.Advance(time.Hour)
	}

	removed, err := fixture.store.PruneBackups(fixture.backupDir, 3)
	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d backups, want 2", len(removed))
	}
	// paths[0] and paths[1] are the two oldest.
	for _, want := range paths[:2] {
		if !contains(removed, want) {
			t.Errorf("removed = %v, want it to include the oldest backup %s", removed, want)
		}
	}
	for _, keep := range paths[2:] {
		if contains(removed, keep) {
			t.Errorf("removed = %v, want %s kept: it is one of the 3 newest", removed, keep)
		}
	}

	remaining, err := fixture.store.ListBackups(fixture.backupDir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(remaining) != 3 {
		t.Errorf("got %d backups remaining, want 3", len(remaining))
	}
}

// ADR-15: pre-migration backups are kept outside the rotation and are never
// counted against keep, however many rotation backups pile up.
func TestPruneBackups_NeverTouchesPreMigrationBackups(t *testing.T) {
	fixture := newMigrationFixture(t)
	preMigration, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupPreMigration)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	fixture.clock.Advance(time.Hour)
	for range 5 {
		if _, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation); err != nil {
			t.Fatalf("Backup: %v", err)
		}
		fixture.clock.Advance(time.Hour)
	}

	if _, err := fixture.store.PruneBackups(fixture.backupDir, 3); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}

	remaining, err := fixture.store.ListBackups(fixture.backupDir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	found := false
	for _, backup := range remaining {
		if backup.Path == preMigration {
			found = true
		}
	}
	if !found {
		t.Error("the pre-migration backup was pruned, want it untouched by rotation")
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
