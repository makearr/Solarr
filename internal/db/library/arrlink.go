package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ArrLink is one *Arr item and what it is here.
type ArrLink struct {
	SourceKind, SourceID string
	// LocalKind is "work", "person" or "organisation".
	LocalKind string
	LocalID   int64
	// SyncedMonitored is the monitored flag last pushed to or pulled from the *Arr; nil before either happened.
	SyncedMonitored *bool
}

// ArrLinks keeps which work, person or organisation each item of a connected *Arr is, for sending monitoring back.
type ArrLinks struct{ store *store.Store }

// NewArrLinks builds it over an open library store.
func NewArrLinks(opened *store.Store) ArrLinks { return ArrLinks{store: opened} }

// Link records what an *Arr item is here; a later import's answer replaces an earlier one.
func (l ArrLinks) Link(ctx context.Context, instanceID int64, link ArrLink) error {
	err := l.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).UpsertArrLink(ctx, librarydb.UpsertArrLinkParams{
			InstanceID: instanceID, SourceKind: link.SourceKind, SourceID: link.SourceID, LocalKind: link.LocalKind, LocalID: link.LocalID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: link %s %s of instance %d: %w", link.SourceKind, link.SourceID, instanceID, err)
	}
	return nil
}

// SetSynced remembers the monitored flag both sides now agree on, so the next run can tell which side changed.
func (l ArrLinks) SetSynced(ctx context.Context, instanceID int64, link ArrLink, monitored bool) error {
	err := l.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetArrLinkSynced(ctx, librarydb.SetArrLinkSyncedParams{
			SyncedMonitored: sql.NullInt64{Int64: boolToInt(monitored), Valid: true},
			InstanceID:      instanceID, SourceKind: link.SourceKind, SourceID: link.SourceID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: record sync of %s %s of instance %d: %w", link.SourceKind, link.SourceID, instanceID, err)
	}
	return nil
}

// ForInstance lists every item of one connection that an import matched.
func (l ArrLinks) ForInstance(ctx context.Context, instanceID int64) ([]ArrLink, error) {
	rows, err := librarydb.New(l.store.Read()).ListArrLinksForInstance(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("library: links of instance %d: %w", instanceID, err)
	}
	links := make([]ArrLink, 0, len(rows))
	for _, row := range rows {
		link := ArrLink{SourceKind: row.SourceKind, SourceID: row.SourceID, LocalKind: row.LocalKind, LocalID: row.LocalID}
		if row.SyncedMonitored.Valid {
			synced := row.SyncedMonitored.Int64 == 1
			link.SyncedMonitored = &synced
		}
		links = append(links, link)
	}
	return links, nil
}

// Forget removes every link of a deleted connection.
func (l ArrLinks) Forget(ctx context.Context, instanceID int64) error {
	err := l.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteArrLinksForInstance(ctx, instanceID)
	})
	if err != nil {
		return fmt.Errorf("library: forget links of instance %d: %w", instanceID, err)
	}
	return nil
}
