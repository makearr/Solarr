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
