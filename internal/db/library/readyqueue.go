package library

import (
	"context"
	"database/sql"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ReadyEntry is one held release (ADR-57's HELD): decided, not sent, because its client's queue was full.
type ReadyEntry struct {
	CandidateID   int64
	DesiredItemID int64
	Priority      int64
	QueuedAt      int64
	ExpiresAt     int64
	// Origin is who asked: "user", "rss" or "automatic".
	Origin string
}

// ReadyQueue is section 10.5's ready queue: held releases, sent the moment a client has room.
type ReadyQueue struct{ store *store.Store }

// NewReadyQueue builds it over an open library store.
func NewReadyQueue(opened *store.Store) ReadyQueue { return ReadyQueue{store: opened} }

// Hold records entry, replacing any release held earlier for the same album.
func (r ReadyQueue) Hold(ctx context.Context, entry ReadyEntry) error {
	return r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DequeueReadyForItem(ctx, entry.DesiredItemID); err != nil {
			return err
		}
		return queries.EnqueueReady(ctx, librarydb.EnqueueReadyParams{
			CandidateID: entry.CandidateID, DesiredItemID: entry.DesiredItemID, Priority: entry.Priority,
			QueuedAt: entry.QueuedAt, ExpiresAt: entry.ExpiresAt, Origin: entry.Origin,
		})
	})
}

// Next answers up to limit unexpired entries, highest priority first, then longest waiting.
func (r ReadyQueue) Next(ctx context.Context, now int64, limit int) ([]ReadyEntry, error) {
	rows, err := librarydb.New(r.store.Read()).NextReady(ctx, librarydb.NextReadyParams{Now: now, PageSize: int64(limit)})
	if err != nil {
		return nil, err
	}
	entries := make([]ReadyEntry, len(rows))
	for i, row := range rows {
		entries[i] = ReadyEntry{
			CandidateID: row.CandidateID, DesiredItemID: row.DesiredItemID, Priority: row.Priority,
			QueuedAt: row.QueuedAt, ExpiresAt: row.ExpiresAt, Origin: row.Origin,
		}
	}
	return entries, nil
}

// Remove drops one entry: sent, or no longer worth sending.
func (r ReadyQueue) Remove(ctx context.Context, candidateID int64) error {
	return r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DequeueReady(ctx, candidateID)
	})
}

// PurgeExpired drops entries that waited past their candidate's expiry; they are searched afresh.
func (r ReadyQueue) PurgeExpired(ctx context.Context, now int64) error {
	return r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).PurgeExpiredReady(ctx, now)
	})
}
