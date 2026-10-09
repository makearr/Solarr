package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Sightings remember when RSS showed a release for each wanted item: what "fresh" reads (ADR-55, first seen on an
// indexer within 48 h), kept here because the candidates themselves are not read to decide.
type Sightings struct{ store *store.Store }

// NewSightings builds it over an open library store.
func NewSightings(opened *store.Store) Sightings { return Sightings{store: opened} }

// RecordRSS notes that RSS showed a release for the item, first seen at seenAt; the newest is kept.
func (s Sightings) RecordRSS(ctx context.Context, desiredItemID, seenAt int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordRSSSighting(ctx, librarydb.RecordRSSSightingParams{DesiredItemID: desiredItemID, SeenAt: seenAt})
	})
	if err != nil {
		return fmt.Errorf("library: rss sighting of item %d: %w", desiredItemID, err)
	}
	return nil
}
