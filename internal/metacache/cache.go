// Package metacache is the permanent metadata cache behind cache/metadata.db
// (ADR-2, ADR-29). It is deliberately generic -- kind and external id are opaque
// strings the caller defines, and the body is whatever JSON the caller wants back --
// so every metadata provider shares one cache rather than each growing its own.
// Disposable: nothing here is authoritative, so there is no backup rotation and a
// miss just costs a re-fetch, never data.
package metacache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/metadatadb"
)

// ErrNotFound reports a cache miss.
var ErrNotFound = errors.New("metacache: not found")

// Entry is one cached answer.
type Entry struct {
	// Source is who answered -- informational only, not part of the cache key, so
	// a provider failing over to another one (ADR-29) still hits the cache the
	// first provider populated.
	Source    string
	Body      string
	FetchedAt time.Time
}

// Cache is metadata.db.
type Cache struct{ store *store.Store }

// New builds it over an open metadata store.
func New(opened *store.Store) Cache { return Cache{store: opened} }

// Get returns the cached entry for (kind, externalID), or ErrNotFound.
func (c Cache) Get(ctx context.Context, kind, externalID string) (Entry, error) {
	row, err := metadatadb.New(c.store.Read()).GetCachedMetadata(ctx, metadatadb.GetCachedMetadataParams{
		Kind: kind, ExternalID: externalID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("metacache: get %s:%s: %w", kind, externalID, err)
	}
	return Entry{Source: row.Source, Body: row.Body, FetchedAt: time.Unix(row.FetchedAt, 0)}, nil
}

// Set stores (or replaces) the entry for (kind, externalID).
func (c Cache) Set(ctx context.Context, kind, externalID string, entry Entry) error {
	return c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return metadatadb.New(tx).SetCachedMetadata(ctx, metadatadb.SetCachedMetadataParams{
			Kind: kind, ExternalID: externalID, Source: entry.Source,
			Body: entry.Body, FetchedAt: entry.FetchedAt.Unix(),
		})
	})
}
