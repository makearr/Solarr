package store_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
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
	if report.SizeAfter >= report.SizeBefore || report.SizeAfter > 1<<20 {
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
