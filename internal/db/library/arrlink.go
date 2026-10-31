package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
)

// ArrLink is one *Arr item and what it is here.
type ArrLink struct {
	SourceKind, SourceID string
	// LocalKind is "work", "person" or "organisation".
	LocalKind string
	LocalID   int64
}

// ArrLinks keeps which work, person or organisation each item of a connected *Arr is, for sending monitoring back.
type ArrLinks struct{ store *store.Store }

// NewArrLinks builds it over an open library store.
func NewArrLinks(opened *store.Store) ArrLinks { return ArrLinks{store: opened} }

// Link records what an *Arr item is here; a later import's answer replaces an earlier one.
func (l ArrLinks) Link(ctx context.Context, instanceID int64, link ArrLink) error {
	err := l.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO arr_link (instance_id, source_kind, source_id, local_kind, local_id) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (instance_id, source_kind, source_id) DO UPDATE SET local_kind = excluded.local_kind, local_id = excluded.local_id`,
			instanceID, link.SourceKind, link.SourceID, link.LocalKind, link.LocalID)
		return err
	})
	if err != nil {
		return fmt.Errorf("library: link %s %s of instance %d: %w", link.SourceKind, link.SourceID, instanceID, err)
	}
	return nil
}

// ForInstance lists every item of one connection that an import matched.
func (l ArrLinks) ForInstance(ctx context.Context, instanceID int64) ([]ArrLink, error) {
	rows, err := l.store.Read().QueryContext(ctx, `SELECT source_kind, source_id, local_kind, local_id FROM arr_link WHERE instance_id = ? ORDER BY source_kind, source_id`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("library: links of instance %d: %w", instanceID, err)
	}
	defer func() { _ = rows.Close() }()
	var links []ArrLink
	for rows.Next() {
		var link ArrLink
		if err := rows.Scan(&link.SourceKind, &link.SourceID, &link.LocalKind, &link.LocalID); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}
