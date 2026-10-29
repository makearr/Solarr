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

// ListOutcome is what an import list made of an entry, kept so the entry is not handled again.
type ListOutcome string

// The outcomes kept; an entry excluded or filtered is checked again each run, as either can be lifted.
const (
	ListAdded      ListOutcome = "added"
	ListHeld       ListOutcome = "held"
	ListForeign    ListOutcome = "foreign"
	ListUnresolved ListOutcome = "unresolved"
)

// ImportListEntry is one entry an import list handled.
type ImportListEntry struct {
	ListID               int64
	Provider, ExternalID string
	Outcome              ListOutcome
	WorkID               int64
	Title                string
	SeenAt               int64
}

// ImportListState is an import list's last preview and last run; zero times mean never.
type ImportListState struct {
	ListID int64
	// PreviewedOptions is the type and settings the last preview read, in the caller's encoding.
	PreviewedOptions string
	PreviewedAt      int64
	RunAt            int64
	RunAdded         int64
	// RunFailure names why the last run stopped, empty when it finished.
	RunFailure string
}

// ImportLists is what every import list handled and its runs.
type ImportLists struct{ store *store.Store }

// NewImportLists wraps an opened library database.
func NewImportLists(opened *store.Store) ImportLists { return ImportLists{store: opened} }

// Entries answers what one list handled.
func (l ImportLists) Entries(ctx context.Context, listID int64) ([]ImportListEntry, error) {
	rows, err := librarydb.New(l.store.Read()).ListImportListEntries(ctx, listID)
	if err != nil {
		return nil, fmt.Errorf("library: import list %d entries: %w", listID, err)
	}
	entries := make([]ImportListEntry, len(rows))
	for i, row := range rows {
		entries[i] = ImportListEntry{
			ListID: row.ListID, Provider: row.Provider, ExternalID: row.ExternalID, Outcome: ListOutcome(row.Outcome),
			WorkID: row.WorkID, Title: row.Title, SeenAt: row.SeenAt,
		}
	}
	return entries, nil
}

// Record keeps what lists made of entries, in bounded batches.
func (l ImportLists) Record(ctx context.Context, entries []ImportListEntry) error {
	err := store.WriteBatched(ctx, l.store, store.PrioNormal, recordBatch, entries, func(ctx context.Context, tx *sql.Tx, batch []ImportListEntry) error {
		queries := librarydb.New(tx)
		for _, entry := range batch {
			if err := queries.RecordImportListEntry(ctx, librarydb.RecordImportListEntryParams{
				ListID: entry.ListID, Provider: entry.Provider, ExternalID: entry.ExternalID, Outcome: string(entry.Outcome),
				WorkID: entry.WorkID, Title: entry.Title, SeenAt: entry.SeenAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: record import list entries: %w", err)
	}
	return nil
}

// recordBatch is the rows one write keeps (Invariant: bulk work in 200-row chunks).
const recordBatch = 200

// State answers one list's last preview and run; a list never previewed nor run has the zero state.
func (l ImportLists) State(ctx context.Context, listID int64) (ImportListState, error) {
	row, err := librarydb.New(l.store.Read()).GetImportListState(ctx, listID)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportListState{ListID: listID}, nil
	}
	if err != nil {
		return ImportListState{}, fmt.Errorf("library: import list %d state: %w", listID, err)
	}
	return ImportListState{
		ListID: row.ListID, PreviewedOptions: row.PreviewedOptions, PreviewedAt: row.PreviewedAt, RunAt: row.RunAt,
		RunAdded: row.RunAdded, RunFailure: row.RunFailure,
	}, nil
}

// RecordPreview keeps what a list's preview read, which enabling the list checks.
func (l ImportLists) RecordPreview(ctx context.Context, listID int64, at time.Time, previewed string) error {
	err := l.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordImportListPreview(ctx, librarydb.RecordImportListPreviewParams{
			ListID: listID, PreviewedOptions: previewed, PreviewedAt: at.Unix(),
		})
	})
	if err != nil {
		return fmt.Errorf("library: record import list %d preview: %w", listID, err)
	}
	return nil
}

// RecordRun keeps a list's last run: when, how many works it added, and why it stopped if it did.
func (l ImportLists) RecordRun(ctx context.Context, listID int64, at time.Time, added int, failure string) error {
	err := l.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordImportListRun(ctx, librarydb.RecordImportListRunParams{
			ListID: listID, RunAt: at.Unix(), RunAdded: int64(added), RunFailure: failure,
		})
	})
	if err != nil {
		return fmt.Errorf("library: record import list %d run: %w", listID, err)
	}
	return nil
}

// Forget drops a deleted list's entries and state; the works it added stay.
func (l ImportLists) Forget(ctx context.Context, listID int64) error {
	err := l.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DeleteImportListEntries(ctx, listID); err != nil {
			return err
		}
		return queries.DeleteImportListState(ctx, listID)
	})
	if err != nil {
		return fmt.Errorf("library: forget import list %d: %w", listID, err)
	}
	return nil
}
