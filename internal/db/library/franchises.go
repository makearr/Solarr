package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// FranchiseKind is the collection kind of a movie franchise a provider names (TMDB's collections).
const FranchiseKind = "franchise"

// Franchises are the collections of whole works a provider groups: the films of one franchise.
type Franchises struct{ store *store.Store }

// NewFranchises builds it over an open library store.
func NewFranchises(opened *store.Store) Franchises { return Franchises{store: opened} }

// Join puts a work in the franchise a provider names, creating it when new and keeping its name current; a franchise a
// person monitored stays monitored. It answers the collection's id.
func (f Franchises) Join(ctx context.Context, mediaType, provider, externalID, name string, workID int64) (int64, error) {
	var id int64
	err := f.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		return joinFranchiseTx(ctx, queries, &id, f.store.Now().Unix(), mediaType, provider, externalID, name, workID)
	})
	if err != nil {
		return 0, fmt.Errorf("library: join franchise %s:%s: %w", provider, externalID, err)
	}
	return id, nil
}

func joinFranchiseTx(
	ctx context.Context, queries *librarydb.Queries, id *int64, now int64, mediaType, provider, externalID, name string, workID int64,
) error {
	found, err := queries.GetCollectionByExternalID(ctx, librarydb.GetCollectionByExternalIDParams{Provider: provider, ExternalID: externalID})
	switch {
	case err == nil:
		*id = found.ID
		if name != "" && found.Name != name {
			if err := queries.SetCollectionName(ctx, librarydb.SetCollectionNameParams{Name: name, ID: found.ID}); err != nil {
				return err
			}
		}
	case errors.Is(err, sql.ErrNoRows):
		created, err := queries.CreateCollection(ctx, librarydb.CreateCollectionParams{
			MediaType: mediaType, Name: name, Kind: FranchiseKind, Source: provider, AddedAt: now,
		})
		if err != nil {
			return err
		}
		*id = created.ID
		if err := queries.LinkCollectionExternalID(ctx, librarydb.LinkCollectionExternalIDParams{Provider: provider, ExternalID: externalID, CollectionID: created.ID}); err != nil {
			return err
		}
	default:
		return err
	}
	return queries.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{CollectionID: *id, WorkID: workID, PartID: 0})
}

// SetMonitoredOf sets the monitored flag of every franchise a work belongs to.
func (f Franchises) SetMonitoredOf(ctx context.Context, workID int64, monitored bool) error {
	err := f.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		collections, err := queries.ListCollectionsForWork(ctx, workID)
		if err != nil {
			return err
		}
		for _, collection := range collections {
			if collection.Kind != FranchiseKind {
				continue
			}
			if err := queries.SetCollectionMonitored(ctx, librarydb.SetCollectionMonitoredParams{Monitored: boolToInt(monitored), ID: collection.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: monitor franchises of work %d: %w", workID, err)
	}
	return nil
}
