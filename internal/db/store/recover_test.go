package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

// recoveryFixture owns the database's whole lifecycle, because recovery is a
// sequence of opens and closes rather than one long-lived store.
type recoveryFixture struct {
	path      string
	backupDir string
	clock     *clock.Fake
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	directory := t.TempDir()
	fixture := &recoveryFixture{
		path:      filepath.Join(directory, "library.db"),
		backupDir: filepath.Join(directory, "backups"),
		clock:     clock.NewFakeAtEpoch(),
	}
	if err := os.MkdirAll(fixture.backupDir, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	return fixture
}

func (f *recoveryFixture) config() store.Config {
	return store.Config{Name: "library", Path: f.path, Logger: quietLogger()}
}

func (f *recoveryFixture) open(t *testing.T) *store.Store {
	t.Helper()
	opened, err := store.Open(t.Context(), f.clock, f.config())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return opened
}

// seed writes enough rows to spread the table over several pages, so corrupting
// one of them leaves page 1 and the schema intact and quick_check has to find it.
func (f *recoveryFixture) seed(t *testing.T, note string) {
	t.Helper()
	opened := f.open(t)
	defer func() {
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}()

	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"CREATE TABLE IF NOT EXISTS note (id INTEGER PRIMARY KEY, text TEXT NOT NULL)"); err != nil {
			return err
		}
		for range 200 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO note (text) VALUES (?)", note+strings.Repeat("x", 200)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed %q: %v", note, err)
	}
}

func (f *recoveryFixture) backup(t *testing.T) string {
	t.Helper()
	opened := f.open(t)
	defer func() { _ = opened.Close() }()
	path, err := opened.Backup(t.Context(), f.backupDir, store.BackupRotation)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	return path
}

// notes reports which seeds survived, which is how a test says the right backup
// was the one restored.
func (f *recoveryFixture) notes(t *testing.T, opened *store.Store) map[string]int {
	t.Helper()
	rows, err := opened.Read().QueryContext(t.Context(),
		"SELECT substr(text, 1, 6) AS label, count(*) FROM note GROUP BY label")
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := map[string]int{}
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			t.Fatalf("scan note: %v", err)
		}
		found[label] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read notes: %v", err)
	}
	return found
}

// corrupt scribbles over a page past the first, leaving the header and schema
// readable so the file still opens and only the integrity pragma objects.
func corrupt(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open %s to corrupt: %v", path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	const pageSize = 4096
	if info.Size() < 4*pageSize {
		t.Fatalf("%s is %d bytes, too small to corrupt a page past the first", path, info.Size())
	}
	garbage := make([]byte, pageSize)
	for index := range garbage {
		garbage[index] = 0xFF
	}
	if _, err := file.WriteAt(garbage, 2*pageSize); err != nil {
		t.Fatalf("corrupt %s: %v", path, err)
	}
}

func TestOpenChecked_OpensADatabaseThatIsNotThereYet(t *testing.T) {
	fixture := newRecoveryFixture(t)

	opened, report, err := store.OpenChecked(t.Context(), fixture.clock, fixture.config(),
		store.RecoveryOptions{BackupDir: fixture.backupDir})
	if err != nil {
		t.Fatalf("OpenChecked: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if report.Recovered {
		t.Errorf("a fresh install reported recovery: %+v", report)
	}
	// The check must not be what creates the file, or it beats createRestricted
	// to the mode and the database exists world-readable (Invariant 68).
	info, err := os.Stat(fixture.path)
	if err != nil {
		t.Fatalf("stat the new database: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("new database is %v, want 0600", info.Mode().Perm())
	}
}

func TestOpenChecked_LeavesAHealthyDatabaseAlone(t *testing.T) {
	fixture := newRecoveryFixture(t)
	fixture.seed(t, "first1")
	fixture.backup(t)

	opened, report, err := store.OpenChecked(t.Context(), fixture.clock, fixture.config(),
		store.RecoveryOptions{BackupDir: fixture.backupDir})
	if err != nil {
		t.Fatalf("OpenChecked: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if report.Recovered || report.CorruptPath != "" {
		t.Errorf("a healthy database was recovered: %+v", report)
	}
	if got := fixture.notes(t, opened); got["first1"] != 200 {
		t.Errorf("notes = %v, want first1:200", got)
	}
}

func TestOpenChecked_RestoresTheNewestBackupAndKeepsTheDamagedFile(t *testing.T) {
	fixture := newRecoveryFixture(t)
	fixture.seed(t, "oldest")
	oldest := fixture.backup(t)
	fixture.clock.Advance(time.Hour)
	fixture.seed(t, "newer1")
	newest := fixture.backup(t)
	fixture.clock.Advance(time.Hour)
	fixture.seed(t, "unsave")
	corrupt(t, fixture.path)

	opened, report, err := store.OpenChecked(t.Context(), fixture.clock, fixture.config(),
		store.RecoveryOptions{BackupDir: fixture.backupDir})
	if err != nil {
		t.Fatalf("OpenChecked: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if !report.Recovered || report.From != newest {
		t.Fatalf("report = %+v, want recovery from %s", report, newest)
	}
	if len(report.Rejected) != 0 {
		t.Errorf("rejected %v, want none", report.Rejected)
	}
	if _, err := os.Stat(report.CorruptPath); err != nil {
		t.Errorf("damaged database not kept at %s: %v", report.CorruptPath, err)
	}
	// The unbacked-up seed is exactly the history ADR-15 says the notification
	// has to admit was lost.
	got := fixture.notes(t, opened)
	if got["oldest"] != 200 || got["newer1"] != 200 || got["unsave"] != 0 {
		t.Errorf("notes = %v, want oldest:200 newer1:200 and no unsave; oldest backup was %s", got, oldest)
	}
	if lost := report.HistoryLost(fixture.clock.Now()); lost != time.Hour {
		t.Errorf("HistoryLost = %s, want 1h", lost)
	}
}

func TestOpenChecked_WalksPastABackupThatIsCorruptToo(t *testing.T) {
	fixture := newRecoveryFixture(t)
	fixture.seed(t, "oldest")
	oldest := fixture.backup(t)
	fixture.clock.Advance(time.Hour)
	fixture.seed(t, "newer1")
	newest := fixture.backup(t)
	corrupt(t, newest)
	corrupt(t, fixture.path)

	opened, report, err := store.OpenChecked(t.Context(), fixture.clock, fixture.config(),
		store.RecoveryOptions{BackupDir: fixture.backupDir})
	if err != nil {
		t.Fatalf("OpenChecked: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if report.From != oldest {
		t.Fatalf("restored %s, want the next-oldest %s", report.From, oldest)
	}
	if len(report.Rejected) != 1 || report.Rejected[0] != newest {
		t.Errorf("rejected %v, want [%s]", report.Rejected, newest)
	}
	if got := fixture.notes(t, opened); got["newer1"] != 0 {
		t.Errorf("notes = %v, want nothing from the corrupt backup", got)
	}
}

func TestOpenChecked_RefusesToStartWhenNoBackupIsUsable(t *testing.T) {
	fixture := newRecoveryFixture(t)
	fixture.seed(t, "oldest")
	only := fixture.backup(t)
	corrupt(t, only)
	// Not a database at all, which is what a truncated or zeroed file looks like:
	// SQLite never gets far enough to touch the log, so quarantine has one to move.
	if err := os.WriteFile(fixture.path, []byte("this is not a database"), 0o600); err != nil {
		t.Fatalf("replace the database with garbage: %v", err)
	}
	if err := os.WriteFile(fixture.path+"-wal", []byte("left by the crash"), 0o600); err != nil {
		t.Fatalf("write crash log: %v", err)
	}

	opened, report, err := store.OpenChecked(t.Context(), fixture.clock, fixture.config(),
		store.RecoveryOptions{BackupDir: fixture.backupDir})
	if opened != nil {
		_ = opened.Close()
		t.Fatal("OpenChecked returned a store after exhausting every backup")
	}
	if !errors.Is(err, store.ErrUnrecoverable) {
		t.Fatalf("err = %v, want ErrUnrecoverable", err)
	}
	if _, statErr := os.Stat(report.CorruptPath); statErr != nil {
		t.Errorf("damaged database not kept at %q: %v", report.CorruptPath, statErr)
	}
	// The driver discards the unreadable log when probe opens the file, so nothing
	// is left at the database path for the next startup to find.
	if _, statErr := os.Stat(fixture.path + "-wal"); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("log left at the database path with no database: %v", statErr)
	}
}

func TestOpenChecked_WithoutABackupDirectoryLeavesTheDamagedFileInPlace(t *testing.T) {
	fixture := newRecoveryFixture(t)
	fixture.seed(t, "oldest")
	corrupt(t, fixture.path)

	opened, report, err := store.OpenChecked(t.Context(), fixture.clock, fixture.config(), store.RecoveryOptions{})
	if opened != nil {
		_ = opened.Close()
		t.Fatal("OpenChecked returned a store with nothing to recover from")
	}
	if !errors.Is(err, store.ErrUnrecoverable) {
		t.Fatalf("err = %v, want ErrUnrecoverable", err)
	}
	if report.CorruptPath != "" {
		t.Errorf("quarantined to %q with no backup to put in its place", report.CorruptPath)
	}
	if _, statErr := os.Stat(fixture.path); statErr != nil {
		t.Errorf("database moved despite nothing replacing it: %v", statErr)
	}
}

func TestCheck_PassesOnAHealthyDatabaseAndFailsOnADamagedOne(t *testing.T) {
	fixture := newRecoveryFixture(t)
	fixture.seed(t, "first1")

	opened := fixture.open(t)
	if err := opened.Check(t.Context(), false); err != nil {
		t.Errorf("Check on a healthy database: %v", err)
	}
	if err := opened.Check(t.Context(), true); err != nil {
		t.Errorf("deep Check on a healthy database: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	corrupt(t, fixture.path)
	damaged := fixture.open(t)
	defer func() { _ = damaged.Close() }()
	if err := damaged.Check(t.Context(), false); !errors.Is(err, store.ErrCorrupt) {
		t.Errorf("Check on a damaged database = %v, want ErrCorrupt", err)
	}
}

func TestRestore_ClearsALogLeftByTheFileItReplaced(t *testing.T) {
	fixture := newRecoveryFixture(t)
	fixture.seed(t, "first1")
	backup := fixture.backup(t)

	stale := fixture.path + "-wal"
	if err := os.WriteFile(stale, []byte("not this database's log"), 0o600); err != nil {
		t.Fatalf("write stale log: %v", err)
	}
	if err := store.Restore(fsx.NewOS(), backup, fixture.path); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale log survived the restore: %v", err)
	}
}
