// Package statistics reads and writes statistics.db: what was seen and done. Nothing that decides what to download,
// import, replace or delete reads it, so library.db is correct with it empty (revision 2, D7).
package statistics

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// HistoryEntry is what happened to one download, with what was known at the time.
type HistoryEntry struct {
	ID int64
	At int64
	// Kind is download.HistoryKind's value: download.grabbed, .imported, .failed or .removed.
	Kind                              string
	CategoryID, WorkID, AcquisitionID int64
	ReleaseTitle                      string
	// WorkTitle and LeadName name the work as it was then, so a row whose work is gone still says what it was.
	WorkTitle, LeadName     string
	Indexer, Client, Detail string
	// EventID is the jobs.db event a copied row came from; 0 for a row written here.
	EventID int64
}

// History is the download history, kept for good.
type History struct{ store *store.Store }

// NewHistory builds it over an open statistics store.
func NewHistory(opened *store.Store) History { return History{store: opened} }

// Append records one entry and answers its id.
func (h History) Append(ctx context.Context, entry HistoryEntry) (int64, error) {
	var id int64
	err := h.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = statisticsdb.New(tx).AppendHistory(ctx, appendParams(entry))
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("statistics: append %s: %w", entry.Kind, err)
	}
	return id, nil
}

// List answers one page, newest first, of one work's history or of all with workID 0, cursored by id.
func (h History) List(ctx context.Context, workID, beforeID int64, limit int) ([]HistoryEntry, error) {
	rows, err := statisticsdb.New(h.store.Read()).ListHistory(ctx, statisticsdb.ListHistoryParams{
		WorkID: workID, BeforeID: beforeID, PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("statistics: list history: %w", err)
	}
	entries := make([]HistoryEntry, len(rows))
	for i, row := range rows {
		entries[i] = HistoryEntry{
			ID: row.ID, At: row.At, Kind: row.Kind, CategoryID: row.CategoryID, WorkID: row.WorkID,
			AcquisitionID: row.AcquisitionID, ReleaseTitle: row.ReleaseTitle, WorkTitle: row.WorkTitle,
			LeadName: row.LeadName, Indexer: row.Indexer, Client: row.Client, Detail: row.Detail, EventID: row.EventID,
		}
	}
	return entries, nil
}

// LastCopiedEvent answers the newest jobs.db event copied, where a copy resumes.
func (h History) LastCopiedEvent(ctx context.Context) (int64, error) {
	id, err := statisticsdb.New(h.store.Read()).LastCopiedHistoryEvent(ctx)
	if err != nil {
		return 0, fmt.Errorf("statistics: last copied event: %w", err)
	}
	return id, nil
}

// Copy stores entries copied from jobs.db's events in one transaction; an event already copied is left as it is.
func (h History) Copy(ctx context.Context, entries []HistoryEntry) error {
	err := h.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		for _, entry := range entries {
			if err := queries.CopyHistoryEvent(ctx, statisticsdb.CopyHistoryEventParams(appendParams(entry))); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("statistics: copy history: %w", err)
	}
	return nil
}

func appendParams(entry HistoryEntry) statisticsdb.AppendHistoryParams {
	return statisticsdb.AppendHistoryParams{
		At: entry.At, Kind: entry.Kind, CategoryID: entry.CategoryID, WorkID: entry.WorkID, AcquisitionID: entry.AcquisitionID,
		ReleaseTitle: entry.ReleaseTitle, WorkTitle: entry.WorkTitle, LeadName: entry.LeadName, Indexer: entry.Indexer,
		Client: entry.Client, Detail: entry.Detail, EventID: entry.EventID,
	}
}
