package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ScanProgress is one root folder's sweep state (section 10.4). The zero value is a
// root that has never been swept, which is what a first scan starts from.
type ScanProgress struct {
	RootFolderID int64
	// SweptThrough is when the last completed sweep started. Zero until one has.
	SweptThrough time.Time
	// Cursor is where an interrupted sweep stopped; empty when there is nothing to
	// resume.
	Cursor string

	StartedAt       time.Time
	FinishedAt      time.Time
	FilesFound      int64
	DirectoriesRead int64
}

// Running reports a sweep that started and has not finished, which after a restart
// means one that was interrupted.
func (p ScanProgress) Running() bool {
	return !p.StartedAt.IsZero() && p.FinishedAt.IsZero()
}

// Scans is the scan_state query surface.
type Scans struct{ store *store.Store }

// NewScans builds it over an open library store.
func NewScans(opened *store.Store) Scans { return Scans{store: opened} }

// Progress reports one root's state. A root with no row has never been swept, which
// is not an error -- it is what every root looks like on a first run.
func (s Scans) Progress(ctx context.Context, rootFolderID int64) (ScanProgress, error) {
	row, err := librarydb.New(s.store.Read()).GetScanState(ctx, rootFolderID)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanProgress{RootFolderID: rootFolderID}, nil
	}
	if err != nil {
		return ScanProgress{}, fmt.Errorf("library: scan state for root %d: %w", rootFolderID, err)
	}
	return progressFrom(row), nil
}

// All reports every root that has scan state, which is what an operator view lists.
func (s Scans) All(ctx context.Context) ([]ScanProgress, error) {
	rows, err := librarydb.New(s.store.Read()).ListScanStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list scan state: %w", err)
	}
	progress := make([]ScanProgress, len(rows))
	for index, row := range rows {
		progress[index] = progressFrom(row)
	}
	return progress, nil
}

// Start records that a sweep began. It deliberately does not touch the watermark or
// the cursor: an interrupted sweep must still be resumable, and a sweep that starts
// has proved nothing.
func (s Scans) Start(ctx context.Context, rootFolderID int64, at time.Time) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).StartScan(ctx, librarydb.StartScanParams{
			RootFolderID: rootFolderID, StartedAt: nullableTime(at),
		})
	})
	if err != nil {
		return fmt.Errorf("library: start scan of root %d: %w", rootFolderID, err)
	}
	return nil
}

// Advance records how far a sweep has committed. It runs once per batch, so it is
// one small write rather than a transaction held across the walk (Invariant 2).
func (s Scans) Advance(ctx context.Context, rootFolderID int64, cursor string) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetScanCursor(ctx, librarydb.SetScanCursorParams{
			Cursor: cursor, RootFolderID: rootFolderID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: advance scan of root %d: %w", rootFolderID, err)
	}
	return nil
}

// Finish moves the watermark and clears the cursor. Only a completed sweep may call
// it: the watermark is what says a directory need not be read again.
func (s Scans) Finish(ctx context.Context, rootFolderID int64, result FinishedScan) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).FinishScan(ctx, librarydb.FinishScanParams{
			SweptThrough:    result.SweptThrough.Unix(),
			FinishedAt:      nullableTime(result.FinishedAt),
			FilesFound:      result.FilesFound,
			DirectoriesRead: result.DirectoriesRead,
			RootFolderID:    rootFolderID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: finish scan of root %d: %w", rootFolderID, err)
	}
	return nil
}

// FinishedScan is what a completed sweep leaves behind.
type FinishedScan struct {
	SweptThrough    time.Time
	FinishedAt      time.Time
	FilesFound      int64
	DirectoriesRead int64
}

// Forget removes a root's progress, for a root that has left the configuration.
func (s Scans) Forget(ctx context.Context, rootFolderID int64) error {
	err := s.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteScanState(ctx, rootFolderID)
	})
	if err != nil {
		return fmt.Errorf("library: forget scan state for root %d: %w", rootFolderID, err)
	}
	return nil
}

func progressFrom(row librarydb.ScanState) ScanProgress {
	return ScanProgress{
		RootFolderID:    row.RootFolderID,
		SweptThrough:    unixOrZero(row.SweptThrough),
		Cursor:          row.Cursor,
		StartedAt:       unixOrZeroNullable(row.StartedAt),
		FinishedAt:      unixOrZeroNullable(row.FinishedAt),
		FilesFound:      row.FilesFound,
		DirectoriesRead: row.DirectoriesRead,
	}
}

// unixOrZero keeps "never" as the zero time rather than 1970, so IsZero means what
// it says at every call site.
func unixOrZero(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func unixOrZeroNullable(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return unixOrZero(value.Int64)
}

func nullableTime(at time.Time) sql.NullInt64 {
	if at.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: at.Unix(), Valid: true}
}
