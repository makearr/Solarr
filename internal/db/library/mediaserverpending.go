package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// The changes a media server is told of.
const (
	PendingCreated  = "created"
	PendingModified = "modified"
	PendingDeleted  = "deleted"
)

// pendingChunk bounds one transaction (200 rows).
const pendingChunk = 200

// PendingPush is one path a media server has still to be told about.
type PendingPush struct {
	ID         int64
	ServerID   int64
	CategoryID int64
	Path       string
	Change     string
	WorkID     int64
	QueuedAt   int64
	Attempts   int64
}

// WorkFilePlace is where one file of a work lies, and when it came.
type WorkFilePlace struct {
	RootFolderID int64
	RelPath      string
	CreatedAt    int64
}

// MediaServerPending keeps the queue of paths to push to media servers.
type MediaServerPending struct{ store *store.Store }

// NewMediaServerPending wraps an opened library database.
func NewMediaServerPending(opened *store.Store) MediaServerPending {
	return MediaServerPending{store: opened}
}

// Add queues the rows; a path already queued with the same change stays as it is.
func (m MediaServerPending) Add(ctx context.Context, rows []PendingPush) error {
	for start := 0; start < len(rows); start += pendingChunk {
		chunk := rows[start:min(start+pendingChunk, len(rows))]
		err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, row := range chunk {
				if err := queries.AddMediaServerPending(ctx, librarydb.AddMediaServerPendingParams{
					ServerID: row.ServerID, CategoryID: row.CategoryID, Path: row.Path, Change: row.Change, WorkID: row.WorkID, QueuedAt: row.QueuedAt,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: queue media server paths: %w", err)
		}
	}
	return nil
}

// Due answers up to limit rows of one server queued at or before cutoff, oldest first.
func (m MediaServerPending) Due(ctx context.Context, serverID, cutoff, limit int64) ([]PendingPush, error) {
	rows, err := librarydb.New(m.store.Read()).ListDueMediaServerPending(ctx, librarydb.ListDueMediaServerPendingParams{ServerID: serverID, QueuedAt: cutoff, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("library: due media server paths: %w", err)
	}
	pending := make([]PendingPush, len(rows))
	for i, row := range rows {
		pending[i] = PendingPush(row)
	}
	return pending, nil
}

// ServersDue answers the servers with rows queued at or before cutoff.
func (m MediaServerPending) ServersDue(ctx context.Context, cutoff int64) ([]int64, error) {
	ids, err := librarydb.New(m.store.Read()).ListMediaServerPendingServers(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("library: servers with paths to push: %w", err)
	}
	return ids, nil
}

// Delete removes the rows that were sent.
func (m MediaServerPending) Delete(ctx context.Context, ids []int64) error {
	return m.each(ctx, ids, func(q *librarydb.Queries, encoded string) error { return q.DeleteMediaServerPending(ctx, encoded) })
}

// Bump counts one more failed attempt on the rows.
func (m MediaServerPending) Bump(ctx context.Context, ids []int64) error {
	return m.each(ctx, ids, func(q *librarydb.Queries, encoded string) error { return q.BumpMediaServerPending(ctx, encoded) })
}

// ClearServer drops everything queued for a server that is gone.
func (m MediaServerPending) ClearServer(ctx context.Context, serverID int64) error {
	return m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteMediaServerPendingOf(ctx, serverID)
	})
}

// WorkFiles answers where the files of a work lie.
func (m MediaServerPending) WorkFiles(ctx context.Context, workID int64) ([]WorkFilePlace, error) {
	rows, err := librarydb.New(m.store.Read()).ListWorkFilesForPush(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: files of work %d: %w", workID, err)
	}
	places := make([]WorkFilePlace, len(rows))
	for i, row := range rows {
		places[i] = WorkFilePlace(row)
	}
	return places, nil
}

func (m MediaServerPending) each(ctx context.Context, ids []int64, apply func(*librarydb.Queries, string) error) error {
	for start := 0; start < len(ids); start += pendingChunk {
		encoded, err := json.Marshal(ids[start:min(start+pendingChunk, len(ids))])
		if err != nil {
			return err
		}
		err = m.store.Write(ctx, store.PrioBulk, func(_ context.Context, tx *sql.Tx) error { return apply(librarydb.New(tx), string(encoded)) })
		if err != nil {
			return fmt.Errorf("library: media server paths: %w", err)
		}
	}
	return nil
}
