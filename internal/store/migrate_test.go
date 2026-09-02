package store_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
)

type migrationFixture struct {
	store     *store.Store
	backupDir string
	path      string
	clock     *clock.Fake
}

func newMigrationFixture(t *testing.T) migrationFixture {
	t.Helper()
	directory := t.TempDir()
	fixture := migrationFixture{
		backupDir: filepath.Join(directory, "backups"),
		path:      filepath.Join(directory, "library.db"),
		clock:     clock.NewFakeAtEpoch(),
	}
	if err := os.MkdirAll(fixture.backupDir, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	fixture.store = fixture.open(t)
	return fixture
}

func (f *migrationFixture) open(t *testing.T) *store.Store {
	t.Helper()
	opened, err := store.Open(t.Context(), f.clock, store.Config{
		Name: "library", Path: f.path, Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}

func coreOwner(migrations ...store.Migration) store.Owner {
	return store.Owner{Name: store.CoreOwner, Migrations: migrations}
}

func tableExists(t *testing.T, opened *store.Store, name string) bool {
	t.Helper()
	var count int
	err := opened.Read().QueryRowContext(t.Context(),
		"SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", name).Scan(&count)
	if err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return count == 1
}

func TestMigrate_AppliesInVersionOrder(t *testing.T) {
	fixture := newMigrationFixture(t)

	// Deliberately out of order: version 2 depends on version 1's table.
	owner := coreOwner(
		store.Migration{Version: 2, Up: "ALTER TABLE work ADD COLUMN title TEXT"},
		store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"},
	)
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var columns int
	if err := fixture.store.Read().QueryRowContext(t.Context(),
		"SELECT count(*) FROM pragma_table_info('work')").Scan(&columns); err != nil {
		t.Fatalf("inspect work: %v", err)
	}
	if columns != 2 {
		t.Errorf("work has %d columns, want 2", columns)
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	fixture := newMigrationFixture(t)
	owner := coreOwner(store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"})

	for attempt := range 3 {
		if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, owner); err != nil {
			t.Fatalf("Migrate attempt %d: %v", attempt, err)
		}
	}
	if !tableExists(t, fixture.store, "work") {
		t.Error("work is missing after repeated migration")
	}
}

func TestMigrate_BacksUpOnlyWhenThereIsWorkToDo(t *testing.T) {
	fixture := newMigrationFixture(t)
	owner := coreOwner(store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"})

	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, owner); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	afterFirst, err := os.ReadDir(fixture.backupDir)
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	if len(afterFirst) != 1 {
		t.Fatalf("got %d backups after migrating, want 1 (Invariant 7)", len(afterFirst))
	}

	// A no-op startup must not accumulate a backup every time the process starts.
	fixture.clock.Advance(time.Hour)
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, owner); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	afterSecond, err := os.ReadDir(fixture.backupDir)
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	if len(afterSecond) != 1 {
		t.Errorf("got %d backups after a no-op migration, want 1", len(afterSecond))
	}
}

func TestMigrate_RefusesWithoutABackupDirectory(t *testing.T) {
	fixture := newMigrationFixture(t)
	owner := coreOwner(store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"})

	// Migrations are forward-only, so the backup is the only rollback there is.
	err := store.Migrate(t.Context(), fixture.store, "", owner)
	if !errors.Is(err, store.ErrBackupDirRequired) {
		t.Fatalf("Migrate = %v, want ErrBackupDirRequired", err)
	}
	if tableExists(t, fixture.store, "work") {
		t.Error("the migration ran despite having nowhere to back up to")
	}
}

func TestMigrate_DetectsAnEditedMigration(t *testing.T) {
	fixture := newMigrationFixture(t)
	original := coreOwner(store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"})
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, original); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Editing an applied migration is always a mistake: the schema on disk no
	// longer matches what the code believes it created (§4.4).
	edited := coreOwner(store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY, extra TEXT)"})
	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, edited)
	if !errors.Is(err, store.ErrMigrationChanged) {
		t.Fatalf("Migrate with an edited migration = %v, want ErrMigrationChanged", err)
	}
}

func TestMigrate_AppliesToAPreviousSchemaAsWellAsAnEmptyOne(t *testing.T) {
	// The round-trip §4.4 asks for: version 1 alone, then 1+2 on top of it, must
	// reach the same schema as 1+2 applied to an empty database.
	first := store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"}
	second := store.Migration{Version: 2, Up: "CREATE TABLE edition (id INTEGER PRIMARY KEY, work_id INTEGER NOT NULL)"}

	stepwise := newMigrationFixture(t)
	if err := store.Migrate(t.Context(), stepwise.store, stepwise.backupDir, coreOwner(first)); err != nil {
		t.Fatalf("stepwise v1: %v", err)
	}
	if err := store.Migrate(t.Context(), stepwise.store, stepwise.backupDir, coreOwner(first, second)); err != nil {
		t.Fatalf("stepwise v2: %v", err)
	}

	fresh := newMigrationFixture(t)
	if err := store.Migrate(t.Context(), fresh.store, fresh.backupDir, coreOwner(first, second)); err != nil {
		t.Fatalf("fresh: %v", err)
	}

	if got, want := schemaOf(t, stepwise.store), schemaOf(t, fresh.store); got != want {
		t.Errorf("stepwise schema differs from a fresh one:\n stepwise: %s\n fresh:    %s", got, want)
	}
}

func schemaOf(t *testing.T, opened *store.Store) string {
	t.Helper()
	rows, err := opened.Read().QueryContext(t.Context(),
		"SELECT sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var statements []string
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		statements = append(statements, statement)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema: %v", err)
	}
	return strings.Join(statements, ";\n")
}

func TestMigrate_RejectsOverlappingTablePrefixes(t *testing.T) {
	fixture := newMigrationFixture(t)

	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir,
		store.Owner{Name: "media.tv", TablePrefix: "tv_"},
		store.Owner{Name: "feature.tvguide", TablePrefix: "tv_guide_"},
	)
	if !errors.Is(err, store.ErrPrefixCollision) {
		t.Fatalf("Migrate with overlapping prefixes = %v, want ErrPrefixCollision", err)
	}
}

func TestMigrate_RejectsATableOutsideTheOwnersPrefix(t *testing.T) {
	fixture := newMigrationFixture(t)

	// This is how one plugin ends up owning another's data.
	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, store.Owner{
		Name:        "media.tv",
		TablePrefix: "tv_",
		Migrations:  []store.Migration{{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"}},
	})
	if !errors.Is(err, store.ErrPrefixViolation) {
		t.Fatalf("Migrate = %v, want ErrPrefixViolation", err)
	}
}

func TestMigrate_AcceptsATableInsideTheOwnersPrefix(t *testing.T) {
	fixture := newMigrationFixture(t)

	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, store.Owner{
		Name:        "media.tv",
		TablePrefix: "tv_",
		Migrations:  []store.Migration{{Version: 1, Up: "CREATE TABLE tv_episode (id INTEGER PRIMARY KEY)"}},
	})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !tableExists(t, fixture.store, "tv_episode") {
		t.Error("tv_episode was not created")
	}
}

func TestMigrate_RejectsAPluginWithoutAPrefix(t *testing.T) {
	fixture := newMigrationFixture(t)

	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir,
		store.Owner{Name: "media.tv", Migrations: []store.Migration{{Version: 1, Up: "SELECT 1"}}})
	if err == nil {
		t.Fatal("a plugin owner with no reserved prefix was accepted")
	}
}

func TestMigrate_RejectsDuplicateVersionsWithinAnOwner(t *testing.T) {
	fixture := newMigrationFixture(t)

	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, coreOwner(
		store.Migration{Version: 1, Up: "CREATE TABLE a (id INTEGER PRIMARY KEY)"},
		store.Migration{Version: 1, Up: "CREATE TABLE b (id INTEGER PRIMARY KEY)"},
	))
	if err == nil {
		t.Fatal("two migrations at the same version were accepted")
	}
}

func TestMigrate_AFailingMigrationLeavesNoPartialSchema(t *testing.T) {
	fixture := newMigrationFixture(t)

	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, coreOwner(
		store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY)"},
		store.Migration{Version: 2, Up: "CREATE TABLE edition (id INTEGER PRIMARY KEY); THIS IS NOT SQL"},
	))
	if err == nil {
		t.Fatal("an invalid migration succeeded")
	}
	if !tableExists(t, fixture.store, "work") {
		t.Error("version 1 was rolled back; each migration commits independently")
	}
	if tableExists(t, fixture.store, "edition") {
		t.Error("the failing migration left a partial schema behind")
	}

	// The failure must not be recorded, or a fixed migration would be skipped.
	var recorded int
	if err := fixture.store.Read().QueryRowContext(t.Context(),
		"SELECT count(*) FROM schema_migration WHERE version = 2").Scan(&recorded); err != nil {
		t.Fatalf("read schema_migration: %v", err)
	}
	if recorded != 0 {
		t.Error("a failed migration was recorded as applied")
	}
}

func TestBackup_ProducesAnOpenableCopy(t *testing.T) {
	fixture := newMigrationFixture(t)
	owner := coreOwner(store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY, title TEXT)"})
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	path, err := fixture.store.Backup(t.Context(), fixture.backupDir)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	restored, err := store.Open(t.Context(), fixture.clock, store.Config{Name: "restored", Path: path})
	if err != nil {
		t.Fatalf("open the backup: %v", err)
	}
	defer func() { _ = restored.Close() }()
	if !tableExists(t, restored, "work") {
		t.Error("the backup does not contain the schema")
	}
}

func TestBackup_RejectsARelativeDirectory(t *testing.T) {
	fixture := newMigrationFixture(t)
	if _, err := fixture.store.Backup(t.Context(), "backups"); err == nil {
		t.Error("Backup accepted a relative directory")
	}
}

// Two backups at the same instant must both succeed.
func TestBackup_TwoBackupsInTheSameSecondBothSucceed(t *testing.T) {
	fixture := newMigrationFixture(t)

	first, err := fixture.store.Backup(t.Context(), fixture.backupDir)
	if err != nil {
		t.Fatalf("first Backup: %v", err)
	}
	second, err := fixture.store.Backup(t.Context(), fixture.backupDir)
	if err != nil {
		t.Fatalf("second Backup at the same instant: %v", err)
	}
	if first == second {
		t.Fatalf("both backups chose the same path %q", first)
	}

	entries, err := os.ReadDir(fixture.backupDir)
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d backup files, want 2", len(entries))
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
