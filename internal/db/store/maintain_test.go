package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Maintenance frees what deleted rows left behind and empties the log.
func TestMaintain_VacuumsAndTruncatesTheLog(t *testing.T) {
	path := ""
	opened := openTestStore(t, func(config *store.Config) { path = config.Path })
	filler := strings.Repeat("x", 4000)
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE filler (body TEXT)"); err != nil {
			return err
		}
		for range 500 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO filler (body) VALUES (?)", filler); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM filler")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	report, err := opened.Maintain(t.Context())
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if !report.Vacuumed || report.SizeAfter >= report.SizeBefore || report.SizeAfter > 1<<20 {
		t.Errorf("size %d -> %d, want the deleted rows' pages freed", report.SizeBefore, report.SizeAfter)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() != 0 {
		t.Errorf("log is %d bytes after maintenance, want it truncated", info.Size())
	}
}

// Statistics taken while a table held one row made every join scan it; Analyze must correct them.
func TestAnalyze_ReplacesStatisticsTakenOnATinyTable(t *testing.T) {
	opened := openTestStore(t, nil)
	exec := func(statement string) {
		t.Helper()
		err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, statement)
			return err
		})
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	exec("CREATE TABLE attributes (id INTEGER PRIMARY KEY, codec TEXT)")
	exec("CREATE TABLE objects (id INTEGER PRIMARY KEY, attributes_id INTEGER)")
	exec("INSERT INTO attributes (id, codec) VALUES (1, 'flac')")
	exec("INSERT INTO objects (id, attributes_id) VALUES (1, 1)")
	exec("ANALYZE")
	exec(`WITH RECURSIVE n(i) AS (SELECT 2 UNION ALL SELECT i + 1 FROM n WHERE i < 10000)
		INSERT INTO attributes (id, codec) SELECT i, 'flac' FROM n`)
	exec(`WITH RECURSIVE n(i) AS (SELECT 2 UNION ALL SELECT i + 1 FROM n WHERE i < 10000)
		INSERT INTO objects (id, attributes_id) SELECT i, i FROM n`)

	const query = "SELECT a.codec FROM objects o LEFT JOIN attributes a ON a.id = o.attributes_id WHERE o.id = 5"
	before, err := opened.ExplainQueryPlan(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, "SCAN a") {
		t.Fatalf("precondition: plan with stale statistics is %q, want a scan of the joined table", before)
	}

	if err := opened.Analyze(t.Context()); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	after, err := opened.ExplainQueryPlan(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "SEARCH a USING INTEGER PRIMARY KEY") {
		t.Errorf("plan after Analyze is %q, want a primary-key search of the joined table", after)
	}
}

// A copy of the database (backup, RAM-to-disk snapshot) does not hold the writer: a write issued while it runs completes first,
// and the copy verifies (PERF-19).
func TestSnapshotTo_DoesNotHoldTheWriter(t *testing.T) {
	opened := openTestStore(t, nil)
	write := func(statement string) error {
		return opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, statement)
			return err
		})
	}
	if err := write("CREATE TABLE work (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := write("INSERT INTO work (id) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	holding, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	defer letGo()
	opened.HoldCopies(func() { close(holding); <-release })
	target := filepath.Join(t.TempDir(), "copy.db")
	copied := make(chan error, 1)
	go func() { copied <- opened.SnapshotTo(t.Context(), target) }()
	<-holding

	written := make(chan error, 1)
	go func() { written <- write("INSERT INTO work (id) VALUES (2)") }()
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write during the copy: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a write waited for the copy")
	}
	letGo()
	if err := <-copied; err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}
	if err := store.VerifyFile(t.Context(), fsx.OS{}, "library", target); err != nil {
		t.Errorf("the copy does not verify: %v", err)
	}
}

// A database with next to no free pages is not rebuilt, and one without the room beside it is not either (PERF-20).
func TestMaintain_SkipsTheVacuumThatWouldFreeNothingOrNotFit(t *testing.T) {
	opened := openTestStore(t, nil)
	if err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE kept (body TEXT)"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO kept (body) VALUES (?)", strings.Repeat("x", 100000))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	report, err := opened.Maintain(t.Context())
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if report.Vacuumed || report.VacuumSkipped == "" {
		t.Errorf("Vacuumed = %v, skipped %q; want a skip for a database with no free pages", report.Vacuumed, report.VacuumSkipped)
	}
}

func TestMaintain_DoesNotVacuumWithoutRoomBesideTheDatabase(t *testing.T) {
	opened := openTestStore(t, func(config *store.Config) { config.FS = fullDisk{OS: fsx.NewOS()} })
	if err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE filler (body TEXT)"); err != nil {
			return err
		}
		for range 100 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO filler (body) VALUES (?)", strings.Repeat("x", 4000)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM filler")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	report, err := opened.Maintain(t.Context())
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if report.Vacuumed || !strings.Contains(report.VacuumSkipped, "free space") {
		t.Errorf("Vacuumed = %v, skipped %q; want a skip for lack of room", report.Vacuumed, report.VacuumSkipped)
	}
}

// fullDisk is the real filesystem with one byte free.
type fullDisk struct{ fsx.OS }

func (fullDisk) DiskUsage(string) (totalBytes, freeBytes int64, err error) { return 1 << 30, 1, nil }

// roomDisk is the real filesystem with a set number of bytes free.
type roomDisk struct {
	fsx.OS
	free *int64
}

func (d roomDisk) DiskUsage(string) (totalBytes, freeBytes int64, err error) {
	return 1 << 40, *d.free, nil
}

// A rebuild needs the database's size plus a tenth beside it: just under that is a skip, a little over it vacuums.
func TestMaintain_VacuumsOnlyWithTheDatabasesSizePlusATenthFree(t *testing.T) {
	var free int64
	opened := openTestStore(t, func(config *store.Config) { config.FS = roomDisk{OS: fsx.NewOS(), free: &free} })
	if err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE filler (body TEXT)"); err != nil {
			return err
		}
		for range 100 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO filler (body) VALUES (?)", strings.Repeat("x", 4000)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM filler")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var pages, pageSize int64
	for pragma, into := range map[string]*int64{"page_count": &pages, "page_size": &pageSize} {
		if err := opened.Read().QueryRowContext(t.Context(), "PRAGMA "+pragma).Scan(into); err != nil {
			t.Fatal(err)
		}
	}
	size := pages * pageSize
	free = size + size/20
	short, err := opened.Maintain(t.Context())
	if err != nil || short.Vacuumed || !strings.Contains(short.VacuumSkipped, "free space") {
		t.Fatalf("with %d free of a %d byte database: %+v, %v; want a skip for lack of room", free, size, short, err)
	}
	free = size + size/5
	enough, err := opened.Maintain(t.Context())
	if err != nil || !enough.Vacuumed {
		t.Errorf("with %d free of a %d byte database: %+v, %v; want a vacuum", free, size, enough, err)
	}
}
