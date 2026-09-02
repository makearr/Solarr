// Package store owns database access: one write connection and a read pool per
// file (ADR-3), with every PRAGMA applied per connection (ADR-2).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, keeps CGO_ENABLED=0 building (ADR-5, ADR-30)

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
)

// driverName is the only place the driver is named (ADR-5).
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

	// OnBudgetExceeded is called when a transaction passes WriteHard (Invariant 2).
	OnBudgetExceeded func(TransactionReport)

	// Logger defaults to slog.Default.
	Logger *slog.Logger

	// FS defaults to the real filesystem. It is used for backup bookkeeping —
	// the database itself is written by SQLite, not through here.
	FS fsx.FS
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
	if c.FS == nil {
		c.FS = fsx.NewOS()
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
	fs         fsx.FS
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

	// Created before the driver can, so it never exists world-readable.
	if err := createRestricted(config); err != nil {
		return nil, err
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
		fs:         config.FS,
		queue:      newWriteQueue(),
		writerDone: make(chan struct{}),
	}

	// The write handle goes first: it creates the file, and page_size has to be
	// set before anything writes to it.
	// closeHandles, never Close: the writer goroutine has not started yet, and
	// Close waits on writerDone, which only writeLoop ever closes. Calling it
	// here turns a database that failed to open into a process that hangs.
	if err := write.PingContext(ctx); err != nil {
		_ = store.closeHandles()
		return nil, fmt.Errorf("store %s: %w", config.Name, err)
	}
	if err := read.PingContext(ctx); err != nil {
		_ = store.closeHandles()
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

// Read exposes the read pool.
func (s *Store) Read() *sql.DB { return s.read }

// WriteDB exposes the single write connection (ADR-3).
func (s *Store) WriteDB() *sql.DB { return s.write }

// Close stops accepting writes, drains the queue, and closes both handles (Invariant 2, §15.2).
func (s *Store) Close() error {
	s.queue.close()
	<-s.writerDone

	var errs []error
	// The read pool closes before the checkpoint.
	if s.read != nil {
		if err := s.read.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close read pool: %w", err))
		}
	}
	s.checkpoint()
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

// checkpoint truncates the WAL and updates the query planner's statistics on a clean shutdown
// (section 15.2 step 6).
func (s *Store) checkpoint() {
	for _, statement := range []string{"PRAGMA wal_checkpoint(TRUNCATE)", "PRAGMA optimize"} {
		if _, err := s.write.Exec(statement); err != nil {
			s.logger.Warn("shutdown maintenance",
				slog.String("statement", statement), slog.Any("error", err))
		}
	}
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

// createRestricted makes an empty database file at 0600 so the driver opens one
// that already exists. See documentation/container-runtime.md.
func createRestricted(config Config) error {
	if _, err := config.FS.Stat(config.Path); err == nil {
		// Found, not created: ADR-14 does not enforce a mode on an existing file.
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store %s: check %s: %w", config.Name, config.Path, err)
	}

	file, err := config.FS.CreateSecret(config.Path)
	if err != nil {
		return fmt.Errorf("store %s: create %s: %w", config.Name, config.Path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("store %s: close %s: %w", config.Name, config.Path, err)
	}
	return nil
}
