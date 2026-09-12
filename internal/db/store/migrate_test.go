package store_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/platform/clock"
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

// The F33/4.23 key-widening migration needs a table rebuild that ALTER TABLE cannot express
// (widening a table's own UNIQUE constraint), and rebuilding a table other tables still hold live
// ON DELETE CASCADE references into is exactly what cascades those rows away the instant it is
// dropped -- unless foreign key enforcement is off for the duration. Tested here in isolation,
// with synthetic tables, before the real desired_item migration relies on it.
func TestMigrate_ForeignKeyDisablingMigrationRebuildsWithoutLosingChildren(t *testing.T) {
	fixture := newMigrationFixture(t)

	create := store.Migration{Version: 1, Up: `
CREATE TABLE parent (id INTEGER PRIMARY KEY, x TEXT, UNIQUE(x));
CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id) ON DELETE CASCADE);
`}
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, coreOwner(create)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := fixture.store.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO parent (id, x) VALUES (1, 'a'), (2, 'b')"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO child (id, parent_id) VALUES (10, 1), (11, 2)")
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Widens parent's own UNIQUE(x) to UNIQUE(x, y) -- the same shape desired_item's own
	// rebuild needs, reduced to the smallest schema that exercises the same FK hazard.
	rebuild := store.Migration{Version: 2, DisablesForeignKeys: true, Up: `
CREATE TABLE parent_new (id INTEGER PRIMARY KEY, x TEXT, y TEXT, UNIQUE(x, y));
INSERT INTO parent_new (id, x, y) SELECT id, x, NULL FROM parent;
DROP TABLE parent;
ALTER TABLE parent_new RENAME TO parent;
`}
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, coreOwner(create, rebuild)); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	var childCount int
	if err := fixture.store.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM child").Scan(&childCount); err != nil {
		t.Fatalf("count child: %v", err)
	}
	if childCount != 2 {
		t.Errorf("child rows = %d, want 2 -- the rebuild must not cascade-delete rows referencing the old table", childCount)
	}

	var foreignKeysOn string
	if err := fixture.store.WriteDB().QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeysOn); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeysOn != "1" {
		t.Errorf("foreign_keys = %s on the write connection after the migration, want 1 (Invariant 4)", foreignKeysOn)
	}

	// The whole point: the widened constraint actually admits a second row sharing x now.
	if err := fixture.store.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO parent (id, x, y) VALUES (3, 'a', 'other')")
		return err
	}); err != nil {
		t.Errorf("insert exercising the widened constraint: %v", err)
	}
}

// A migration that claims DisablesForeignKeys but actually leaves a dangling reference (a real
// bug in a future such migration, not the FK mechanism itself) must be refused rather than
// committed silently -- PRAGMA foreign_key_check is the one safety net enforcement being off
// gives up, and this proves it is actually wired in, not just present in a comment.
func TestMigrate_ForeignKeyDisablingMigrationRefusesADanglingReference(t *testing.T) {
	fixture := newMigrationFixture(t)

	create := store.Migration{Version: 1, Up: `
CREATE TABLE parent (id INTEGER PRIMARY KEY, x TEXT, UNIQUE(x));
CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id) ON DELETE CASCADE);
`}
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, coreOwner(create)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := fixture.store.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO parent (id, x) VALUES (1, 'a')"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO child (id, parent_id) VALUES (10, 1)")
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Broken on purpose: copies nothing into parent_new, so child ends up pointing at a
	// row that does not exist in the rebuilt table.
	broken := store.Migration{Version: 2, DisablesForeignKeys: true, Up: `
CREATE TABLE parent_new (id INTEGER PRIMARY KEY, x TEXT);
DROP TABLE parent;
ALTER TABLE parent_new RENAME TO parent;
`}
	err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, coreOwner(create, broken))
	if err == nil {
		t.Fatal("a migration leaving a dangling foreign key was accepted")
	}

	var foreignKeysOn string
	if scanErr := fixture.store.WriteDB().QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeysOn); scanErr != nil {
		t.Fatalf("read foreign_keys: %v", scanErr)
	}
	if foreignKeysOn != "1" {
		t.Errorf("foreign_keys = %s after a refused migration, want 1 -- must be re-enabled even on failure", foreignKeysOn)
	}

	var recorded int
	if err := fixture.store.Read().QueryRowContext(t.Context(),
		"SELECT count(*) FROM schema_migration WHERE version = 2").Scan(&recorded); err != nil {
		t.Fatalf("read schema_migration: %v", err)
	}
	if recorded != 0 {
		t.Error("a migration that failed its own foreign key check was recorded as applied")
	}
}

func TestBackup_ProducesAnOpenableCopy(t *testing.T) {
	fixture := newMigrationFixture(t)
	owner := coreOwner(store.Migration{Version: 1, Up: "CREATE TABLE work (id INTEGER PRIMARY KEY, title TEXT)"})
	if err := store.Migrate(t.Context(), fixture.store, fixture.backupDir, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	path, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation)
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
	if _, err := fixture.store.Backup(t.Context(), "backups", store.BackupRotation); err == nil {
		t.Error("Backup accepted a relative directory")
	}
}

// Two backups at the same instant must both succeed.
func TestBackup_TwoBackupsInTheSameSecondBothSucceed(t *testing.T) {
	fixture := newMigrationFixture(t)

	first, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation)
	if err != nil {
		t.Fatalf("first Backup: %v", err)
	}
	second, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation)
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
