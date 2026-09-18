package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// rowsPerCommit is ADR-3's bulk chunk, so the log grows here the way an import makes
// it grow; commitsUnderTest is well past the threshold, so an unbounded log shows.
const (
	rowsPerCommit    = 200
	commitsUnderTest = 800
	// The limit under test, and the ceiling it settles under. The gap is the writing
	// that continues while a checkpoint runs beside it. The same ceiling is asserted
	// from both sides.
	logLimit   = 2 << 20
	logCeiling = 8 << 20
)

// openAtKnownPath returns the store and its path, which the log lives beside.
func openAtKnownPath(t *testing.T, adjust func(*store.Config)) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.db")
	config := store.Config{Name: "library", Path: path, Logger: quietLogger()}
	if adjust != nil {
		adjust(&config)
	}

	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), config)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	if err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE payload (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
		return err
	}); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return opened, path
}

func fillInCommits(t *testing.T, opened *store.Store, commits int) {
	t.Helper()
	for commit := range commits {
		err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			for row := range rowsPerCommit {
				_, err := tx.ExecContext(ctx, "INSERT INTO payload (body) VALUES (?)",
					fmt.Sprintf("row %d of commit %d, long enough that a page notices it", row, commit))
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("commit %d: %v", commit, err)
		}
	}
}

func logSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatalf("stat the log: %v", err)
	}
	return info.Size()
}

// The writer folds the log back in between transactions, so it settles instead of
// growing for as long as writing continues (documentation/benchmarks.md).
func TestWriter_KeepsTheLogBoundedUnderSustainedWrites(t *testing.T) {
	opened, path := openAtKnownPath(t, func(config *store.Config) {
		config.LogSizeLimitBytes = logLimit
		config.CheckpointEveryCommits = 8
	})
	fillInCommits(t, opened, commitsUnderTest)

	size := logSize(t, path)
	t.Logf("log at %d bytes after %d commits of %d rows", size, commitsUnderTest, rowsPerCommit)
	if size > logCeiling {
		t.Errorf("the log reached %d bytes, over the %d ceiling", size, logCeiling)
	}
}

// The same writes with checkpointing pushed out of reach, to show the test above is
// measuring the checkpoint rather than a log that was never going to grow.
func TestWriter_WithoutCheckpointingTheLogGrowsPastThatBound(t *testing.T) {
	opened, path := openAtKnownPath(t, func(config *store.Config) {
		config.CheckpointEveryCommits = commitsUnderTest * 10
	})
	fillInCommits(t, opened, commitsUnderTest)

	size := logSize(t, path)
	t.Logf("uncheckpointed log at %d bytes", size)
	if size <= logCeiling {
		t.Errorf("the log stayed at %d bytes with checkpointing pushed out of reach, so the test above proves nothing", size)
	}
}

// Without this, SQLite checkpoints inside whichever transaction crosses its threshold,
// which is sometimes one a user is waiting on.
func TestOpen_LeavesAutomaticCheckpointingOff(t *testing.T) {
	opened, _ := openAtKnownPath(t, nil)

	var pages int
	if err := opened.WriteDB().QueryRowContext(t.Context(), "PRAGMA wal_autocheckpoint").Scan(&pages); err != nil {
		t.Fatalf("read wal_autocheckpoint: %v", err)
	}
	if pages != 0 {
		t.Errorf("wal_autocheckpoint is %d, want 0", pages)
	}
}
