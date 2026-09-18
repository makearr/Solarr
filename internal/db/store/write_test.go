package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// openWritableStore wires OnBudgetExceeded to the test, so a transaction over
// the hard budget fails the test rather than only being logged (Invariant 2).
func openWritableStore(t *testing.T, fake *clock.Fake, adjust func(*store.Config)) *store.Store {
	t.Helper()
	config := store.Config{
		Name:   "library",
		Path:   filepath.Join(t.TempDir(), "library.db"),
		Logger: quietLogger(),
		OnBudgetExceeded: func(report store.TransactionReport) {
			t.Errorf("transaction exceeded its hard budget: %s", report)
		},
	}
	if adjust != nil {
		adjust(&config)
	}
	opened, err := store.Open(t.Context(), fake, config)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	if err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE work (id INTEGER PRIMARY KEY, title TEXT NOT NULL)")
		return err
	}); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return opened
}

func countRows(t *testing.T, opened *store.Store) int {
	t.Helper()
	var count int
	if err := opened.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM work").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

func TestWrite_CommitsOnSuccess(t *testing.T) {
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), nil)

	err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES (?)", "Zor A")
		return err
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := countRows(t, opened); got != 1 {
		t.Errorf("row count = %d, want 1", got)
	}
}

func TestWrite_RollsBackOnError(t *testing.T) {
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), nil)
	wantErr := errors.New("planner changed its mind")

	err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES (?)", "Amnesiac"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Write = %v, want %v", err, wantErr)
	}
	if got := countRows(t, opened); got != 0 {
		t.Errorf("row count = %d after a failed transaction, want 0", got)
	}
}

func TestWrite_SerialisesConcurrentCallers(t *testing.T) {
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), nil)

	// One writer per database is what prevents SQLITE_BUSY storms (Invariant 1).
	var group sync.WaitGroup
	for worker := range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			err := opened.Write(context.Background(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES (?)", worker)
				return err
			})
			if err != nil {
				t.Errorf("worker %d: %v", worker, err)
			}
		}()
	}
	group.Wait()

	if got := countRows(t, opened); got != 20 {
		t.Errorf("row count = %d, want 20", got)
	}
}

func TestWriteBatched_ChunksIntoSeparateTransactions(t *testing.T) {
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), nil)
	titles := make([]string, 450)
	for index := range titles {
		titles[index] = "track"
	}

	transactions := 0
	err := store.WriteBatched(t.Context(), opened, store.PrioBulk, 200, titles,
		func(ctx context.Context, tx *sql.Tx, chunk []string) error {
			transactions++
			for _, title := range chunk {
				if _, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES (?)", title); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		t.Fatalf("WriteBatched: %v", err)
	}

	// 450 items at 200 per batch is three transactions, so interactive writes
	// get two chances to interleave rather than none (§4.2).
	if transactions != 3 {
		t.Errorf("ran %d transactions, want 3", transactions)
	}
	if got := countRows(t, opened); got != 450 {
		t.Errorf("row count = %d, want 450", got)
	}
}

func TestWriteBatched_StopsAtTheFailingChunkAndKeepsWhatCommitted(t *testing.T) {
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), nil)
	titles := make([]string, 250)
	wantErr := errors.New("disk vanished")

	call := 0
	err := store.WriteBatched(t.Context(), opened, store.PrioBulk, 100, titles,
		func(ctx context.Context, tx *sql.Tx, chunk []string) error {
			call++
			if call == 3 {
				return wantErr
			}
			for range chunk {
				if _, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES ('x')"); err != nil {
					return err
				}
			}
			return nil
		})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteBatched = %v, want %v", err, wantErr)
	}
	// Each chunk is its own transaction, so the first two survive. That is the
	// trade WriteBatched makes: bounded transactions, not all-or-nothing.
	if got := countRows(t, opened); got != 200 {
		t.Errorf("row count = %d, want 200 from the two committed chunks", got)
	}
}

func TestWriteBatched_RejectsANonPositiveBatchSize(t *testing.T) {
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), nil)

	err := store.WriteBatched(t.Context(), opened, store.PrioBulk, 0, []string{"a"},
		func(context.Context, *sql.Tx, []string) error { return nil })
	if err == nil {
		t.Error("a batch size of 0 was accepted; it would loop forever")
	}
}

func TestWrite_HardBudgetIsReported(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	var reported []store.TransactionReport
	opened := openWritableStore(t, fake, func(config *store.Config) {
		config.WriteWarn = 50 * time.Millisecond
		config.WriteHard = 2 * time.Second
		config.OnBudgetExceeded = func(report store.TransactionReport) {
			reported = append(reported, report)
		}
	})

	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		// The fake clock makes a slow transaction synthetic and deterministic.
		fake.Advance(3 * time.Second)
		_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES ('slow')")
		return err
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(reported) != 1 {
		t.Fatalf("got %d budget reports, want 1 — Invariant 2 is stated but not measured", len(reported))
	}
	if reported[0].Duration != 3*time.Second {
		t.Errorf("Duration = %v, want 3s", reported[0].Duration)
	}
	if reported[0].Budget != 2*time.Second {
		t.Errorf("Budget = %v, want the hard budget of 2s", reported[0].Budget)
	}
	if reported[0].Priority != store.PrioBulk {
		t.Errorf("Priority = %v, want bulk", reported[0].Priority)
	}
}

func TestWrite_WarningBudgetDoesNotFailTheTransaction(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	called := false
	opened := openWritableStore(t, fake, func(config *store.Config) {
		config.WriteWarn = 50 * time.Millisecond
		config.WriteHard = 2 * time.Second
		config.OnBudgetExceeded = func(store.TransactionReport) { called = true }
	})

	err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		fake.Advance(100 * time.Millisecond) // over warn, under hard
		_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES ('warn')")
		return err
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if called {
		t.Error("the warning budget triggered the hard-budget reporter")
	}
	if got := countRows(t, opened); got != 1 {
		t.Errorf("row count = %d; a slow transaction must still commit (ADR-28)", got)
	}
}

func TestWrite_QueuedOperationWithACancelledContextIsDropped(t *testing.T) {
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), nil)

	blocking := make(chan struct{})
	blocked := make(chan struct{})
	go func() {
		_ = opened.Write(context.Background(), store.PrioNormal, func(context.Context, *sql.Tx) error {
			close(blocked)
			<-blocking
			return nil
		})
	}()
	<-blocked

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		queued <- opened.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES ('should not exist')")
			return err
		})
	}()

	for opened.QueueDepth() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	close(blocking)

	// Whether the cancellation is caught before BeginTx or by it does not matter; what must hold
	// is that the caller learns it was cancelled and nothing was written.
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("Write = %v, want context.Canceled", err)
	}
	if got := countRows(t, opened); got != 0 {
		t.Errorf("row count = %d; an abandoned operation was executed anyway", got)
	}
}

func TestClose_DrainsQueuedWritesBeforeClosing(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	path := filepath.Join(t.TempDir(), "library.db")
	opened, err := store.Open(t.Context(), fake, store.Config{Name: "library", Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE work (id INTEGER PRIMARY KEY, title TEXT NOT NULL)")
		return err
	}); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	blocking := make(chan struct{})
	blocked := make(chan struct{})
	go func() {
		_ = opened.Write(context.Background(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
			close(blocked)
			<-blocking
			_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES ('first')")
			return err
		})
	}()
	<-blocked

	queuedDone := make(chan error, 1)
	go func() {
		queuedDone <- opened.Write(context.Background(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES ('second')")
			return err
		})
	}()
	for opened.QueueDepth() == 0 {
		time.Sleep(time.Millisecond)
	}

	closed := make(chan error, 1)
	go func() {
		close(blocking)
		closed <- opened.Close()
	}()

	if err := <-queuedDone; err != nil {
		t.Errorf("the queued write was discarded by Close: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and confirm both writes are on disk.
	reopened, err := store.Open(t.Context(), fake, store.Config{Name: "library", Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if got := countRows(t, reopened); got != 2 {
		t.Errorf("row count = %d after shutdown, want 2 — the drain lost a write", got)
	}
}

func TestWrite_AfterCloseIsRefused(t *testing.T) {
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(t.TempDir(), "library.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err = opened.Write(context.Background(), store.PrioNormal, func(context.Context, *sql.Tx) error { return nil })
	if !errors.Is(err, store.ErrClosed) {
		t.Errorf("Write after Close = %v, want ErrClosed", err)
	}
}
