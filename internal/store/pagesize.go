package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrPageSizeLocked reports a configured page size that does not match an
// existing, non-empty database.
var ErrPageSizeLocked = errors.New("store: page size cannot be changed on a populated database")

// ensurePageSize runs before the WAL handles are opened, on its own connection (ADR-22).
func ensurePageSize(ctx context.Context, config Config) error {
	db, err := sql.Open(driverName, "file:"+config.Path)
	if err != nil {
		return fmt.Errorf("store %s: open for page-size setup: %w", config.Name, err)
	}
	db.SetMaxOpenConns(1)
	// A failed close on the setup connection cannot affect the database: the
	// VACUUM below has already committed by then.
	defer func() { _ = db.Close() }()

	current, err := scanInt(ctx, db, "PRAGMA page_size")
	if err != nil {
		return fmt.Errorf("store %s: read page_size: %w", config.Name, err)
	}
	if current == config.PageSize {
		return nil
	}

	objectCount, err := scanInt(ctx, db, "SELECT count(*) FROM sqlite_schema")
	if err != nil {
		return fmt.Errorf("store %s: inspect schema: %w", config.Name, err)
	}
	if objectCount > 0 {
		return fmt.Errorf("%w: %s is %d bytes per page, config asks for %d; "+
			"changing it requires a full VACUUM, which is not done implicitly at startup",
			ErrPageSizeLocked, config.Name, current, config.PageSize)
	}

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		return fmt.Errorf("store %s: leave WAL for page-size setup: %w", config.Name, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA page_size=%d", config.PageSize)); err != nil {
		return fmt.Errorf("store %s: set page_size: %w", config.Name, err)
	}
	// VACUUM is what actually rewrites the header. The database is empty here,
	// so it is cheap.
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("store %s: vacuum to apply page_size: %w", config.Name, err)
	}

	applied, err := scanInt(ctx, db, "PRAGMA page_size")
	if err != nil {
		return fmt.Errorf("store %s: confirm page_size: %w", config.Name, err)
	}
	if applied != config.PageSize {
		return fmt.Errorf("store %s: page_size is %d after setup, want %d", config.Name, applied, config.PageSize)
	}
	return nil
}

func scanInt(ctx context.Context, db *sql.DB, query string) (int, error) {
	var value int
	if err := db.QueryRowContext(ctx, query).Scan(&value); err != nil {
		return 0, err
	}
	return value, nil
}
