package store_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
)

func openTestStore(t *testing.T, adjust func(*store.Config)) *store.Store {
	t.Helper()
	config := store.Config{
		Name:         "library",
		Path:         filepath.Join(t.TempDir(), "library.db"),
		ReadPoolSize: 4,
	}
	if adjust != nil {
		adjust(&config)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), config)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return opened
}

func TestOpen_AppliesPragmasToEveryPooledConnection(t *testing.T) {
	// Open verifies this already, but it is the point of the package, so it is
	// asserted from outside too.
	opened := openTestStore(t, func(config *store.Config) { config.ReadPoolSize = 4 })

	pinned := make([]*sql.Conn, 0, 4)
	defer func() {
		for _, conn := range pinned {
			_ = conn.Close()
		}
	}()

	for index := range 4 {
		conn, err := opened.Read().Conn(t.Context())
		if err != nil {
			t.Fatalf("pin connection %d: %v", index, err)
		}
		pinned = append(pinned, conn)
	}

	for index, conn := range pinned {
		for _, expectation := range []struct{ pragma, want string }{
			{"journal_mode", "wal"},
			{"foreign_keys", "1"},
			{"synchronous", "1"},
			{"temp_store", "2"},
		} {
			var got string
			if err := conn.QueryRowContext(t.Context(), "PRAGMA "+expectation.pragma).Scan(&got); err != nil {
				t.Fatalf("connection %d, PRAGMA %s: %v", index, expectation.pragma, err)
			}
			if !strings.EqualFold(got, expectation.want) {
				t.Errorf("connection %d has %s=%s, want %s — the DSN reached one connection and not the rest",
					index, expectation.pragma, got, expectation.want)
			}
		}
	}
}

func TestOpen_SetsPageSizeAtCreation(t *testing.T) {
	// page_size cannot be changed after the first write without a full VACUUM
	// (ADR-22), so getting it wrong at creation is permanent.
	opened := openTestStore(t, func(config *store.Config) { config.PageSize = 8192 })

	var pageSize int
	if err := opened.Read().QueryRowContext(t.Context(), "PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatalf("PRAGMA page_size: %v", err)
	}
	if pageSize != 8192 {
		t.Errorf("page_size = %d, want 8192", pageSize)
	}
}

func TestOpen_RejectsARelativePath(t *testing.T) {
	_, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: "relative/library.db",
	})
	if err == nil {
		t.Fatal("Open with a relative path succeeded; paths come from validated config")
	}
}

func TestOpen_CreatesTheDatabaseFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "library.db")

	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{Name: "library", Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if _, err := opened.Read().ExecContext(t.Context(), "CREATE TABLE probe (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("the database is not usable: %v", err)
	}
}

// _txlock=immediate makes a busy database block at BEGIN rather than partway through a
// transaction, turning a mid-transaction SQLITE_BUSY into a clean wait at the start (§4.1).
func TestOpen_WriteHandleTakesTheLockAtBegin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	config := store.Config{
		Name: "library", Path: path,
		BusyTimeout: 150 * time.Millisecond, // short, so the contended BEGIN returns rather than waiting
	}

	first, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), config)
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), config)
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	defer func() { _ = second.Close() }()

	if _, err := first.WriteDB().ExecContext(t.Context(), "CREATE TABLE probe (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	held, err := first.WriteDB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin the holding transaction: %v", err)
	}
	defer func() { _ = held.Rollback() }()

	// With _txlock=immediate this fails or waits here, at BEGIN. Without it,
	// BEGIN succeeds and the failure surfaces later, mid-transaction.
	contended, err := second.WriteDB().BeginTx(t.Context(), nil)
	if err == nil {
		_ = contended.Rollback()
		t.Fatal("a second writer began a transaction while the first held the lock; _txlock=immediate did not apply")
	}
}

func TestStore_ReadPoolServesConcurrentQueries(t *testing.T) {
	opened := openTestStore(t, func(config *store.Config) { config.ReadPoolSize = 4 })

	if _, err := opened.WriteDB().ExecContext(t.Context(), "CREATE TABLE probe (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			var count int
			if err := opened.Read().QueryRowContext(context.Background(), "SELECT count(*) FROM probe").Scan(&count); err != nil {
				errs <- err
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent read: %v", err)
	}
}

func TestStore_NameIsReported(t *testing.T) {
	opened := openTestStore(t, func(config *store.Config) { config.Name = "settings" })
	if got := opened.Name(); got != "settings" {
		t.Errorf("Name() = %q, want %q", got, "settings")
	}
}

func TestOpen_RefusesToChangePageSizeOnAPopulatedDatabase(t *testing.T) {
	// A full VACUUM on a 500k-row library would turn a restart into a
	// multi-minute outage. Refusing is loud; doing it silently is not (ADR-22).
	path := filepath.Join(t.TempDir(), "library.db")

	first, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: path, PageSize: 4096,
	})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := first.WriteDB().ExecContext(t.Context(), "CREATE TABLE probe (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("populate: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: path, PageSize: 8192,
	})
	if !errors.Is(err, store.ErrPageSizeLocked) {
		t.Fatalf("Open with a changed page size = %v, want ErrPageSizeLocked", err)
	}
}

func TestOpen_ReopeningAPopulatedDatabaseKeepsItsPageSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	config := store.Config{Name: "library", Path: path, PageSize: 8192}

	first, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), config)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := first.WriteDB().ExecContext(t.Context(), "CREATE TABLE probe (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("populate: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), config)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = second.Close() }()

	var pageSize int
	if err := second.Read().QueryRowContext(t.Context(), "PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatalf("PRAGMA page_size: %v", err)
	}
	if pageSize != 8192 {
		t.Errorf("page_size = %d after reopening, want 8192", pageSize)
	}
}

// settings.db holds the encrypted credentials, so the ciphertext gets the same
// treatment as the key beside it (ADR-14).
func TestOpen_CreatesTheDatabaseRestricted(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "settings.db")

	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: path, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	// The write-ahead log and shared-memory files carry the same pages, so a
	// restricted database with a world-readable WAL protects nothing.
	for _, name := range []string{"settings.db", "settings.db-wal", "settings.db-shm"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if permissions := info.Mode().Perm(); permissions&0o077 != 0 {
			t.Errorf("%s has mode %04o, readable beyond the owner", name, permissions)
		}
	}
}
