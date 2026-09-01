// Package store owns database access: one write connection and a read pool per
// file (ADR-3), with every PRAGMA applied per connection (ADR-2).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, keeps CGO_ENABLED=0 building (ADR-5, ADR-30)

	"github.com/makearr/MediaNexus/internal/clock"
)

// driverName is the only place the driver is named. ADR-5 calls the choice
// provisional pending a benchmark at 500k rows, so swapping it must be this one
// line plus a test run — no driver-specific SQL or types anywhere else.
const driverName = "sqlite"

// Defaults from §14. Every one is overridable through config.yml.
const (
	defaultReadPoolSize = 4
	defaultBusyTimeout  = 15 * time.Second
	defaultPageSize     = 4096
	defaultCacheSizeKB  = 65536
	defaultWriteWarn    = 50 * time.Millisecond
	defaultWriteHard    = 2 * time.Second
)

// ErrClosed reports use of a store that has been closed.
var ErrClosed = errors.New("store: closed")

// Config describes one database file.
type Config struct {
	// Name identifies the database in logs and metrics: "library", "settings",
	// "jobs", "history".
	Name string
	Path string

	ReadPoolSize int
	BusyTimeout  time.Duration
	// PageSize is applied at creation only and cannot be changed later without
	// a full VACUUM (ADR-22).
	PageSize    int
	CacheSizeKB int

	// WriteWarn logs a transaction that runs long; WriteHard is the ceiling
	// Invariant 2 is measured against.
	WriteWarn time.Duration
	WriteHard time.Duration

	// OnBudgetExceeded is called when a transaction passes WriteHard. Tests wire
	// it to t.Errorf so Invariant 2 is enforced rather than merely logged;
	// production leaves it nil and gets the log and the metric.
	OnBudgetExceeded func(TransactionReport)

	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.ReadPoolSize <= 0 {
		c.ReadPoolSize = defaultReadPoolSize
	}
	if c.BusyTimeout <= 0 {
		c.BusyTimeout = defaultBusyTimeout
	}
	if c.PageSize <= 0 {
		c.PageSize = defaultPageSize
	}
	if c.CacheSizeKB <= 0 {
		c.CacheSizeKB = defaultCacheSizeKB
	}
	if c.WriteWarn <= 0 {
		c.WriteWarn = defaultWriteWarn
	}
	if c.WriteHard <= 0 {
		c.WriteHard = defaultWriteHard
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Store is one database file. Reads go through a pool; writes go through the
// single write connection, and later through the priority queue in front of it.
type Store struct {
	config Config
	clock  clock.Clock

	// write has MaxOpenConns(1) and is owned by the writer goroutine. One writer
	// per file is what prevents SQLITE_BUSY storms (ADR-3, Invariant 1).
	write *sql.DB
	read  *sql.DB

	logger     *slog.Logger
	queue      *writeQueue
	writerDone chan struct{}
}

// Open opens a database file, creating it if absent, and verifies that the
// PRAGMAs actually took effect rather than trusting the DSN.
func Open(ctx context.Context, c clock.Clock, config Config) (*Store, error) {
	config = config.withDefaults()
	if !filepath.IsAbs(config.Path) {
		return nil, fmt.Errorf("store %s: path %q must be absolute", config.Name, config.Path)
	}

	// Settle the page size before anything makes this a WAL database.
	if err := ensurePageSize(ctx, config); err != nil {
		return nil, err
	}

	write, err := sql.Open(driverName, buildDSN(config, true))
	if err != nil {
		return nil, fmt.Errorf("store %s: open write handle: %w", config.Name, err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0) // a pooled connection carries the PRAGMAs; recycling costs and gains nothing

	read, err := sql.Open(driverName, buildDSN(config, false))
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("store %s: open read pool: %w", config.Name, err)
	}
	read.SetMaxOpenConns(config.ReadPoolSize)
	read.SetMaxIdleConns(config.ReadPoolSize)
	read.SetConnMaxLifetime(0)

	store := &Store{
		config:     config,
		clock:      c,
		write:      write,
		read:       read,
		logger:     config.Logger.With(slog.String("database", config.Name)),
		queue:      newWriteQueue(),
		writerDone: make(chan struct{}),
	}

	// The write handle goes first: it creates the file, and page_size has to be
	// set before anything writes to it.
	if err := write.PingContext(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("store %s: %w", config.Name, err)
	}
	if err := read.PingContext(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("store %s: %w", config.Name, err)
	}
	if err := store.verifyPragmas(ctx); err != nil {
		_ = store.closeHandles()
		return nil, err
	}

	go store.writeLoop()
	return store, nil
}

// Name reports which database this is.
func (s *Store) Name() string { return s.config.Name }

// Read exposes the read pool. Repositories query through it; nothing writes
// through it, because a write there would bypass the queue and the one-writer
// rule with it.
func (s *Store) Read() *sql.DB { return s.read }

// WriteDB exposes the single write connection. It is unexported behaviour made
// visible for the migration runner and for tests; ordinary writes go through the
// priority queue, which is the only way the one-writer rule and the transaction
// duration bound stay true (ADR-3).
func (s *Store) WriteDB() *sql.DB { return s.write }

// Close stops accepting writes, drains the queue, and closes both handles. The
// drain is bounded because every transaction is bounded (Invariant 2), which is
// what makes the shutdown deadline in §15.2 safe.
func (s *Store) Close() error {
	s.queue.close()
	<-s.writerDone
	return s.closeHandles()
}

func (s *Store) closeHandles() error {
	var errs []error
	if s.read != nil {
		if err := s.read.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close read pool: %w", err))
		}
	}
	if s.write != nil {
		if err := s.write.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close write handle: %w", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("store %s: %w", s.config.Name, errors.Join(errs...))
	}
	return nil
}
