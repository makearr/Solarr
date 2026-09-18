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

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/metadatadb"
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

// SizeBytes answers the logical bytes currently stored (ADR-60): a real
// measurement of what is there, not the metadata.db file's own physical
// size, which SQLite does not shrink on DELETE without a VACUUM this
// codebase deliberately does not run.
func (c Cache) SizeBytes(ctx context.Context) (int64, error) {
	bytes, err := metadatadb.New(c.store.Read()).SumMetadataCacheBytes(ctx)
	if err != nil {
		return 0, fmt.Errorf("metacache: size: %w", err)
	}
	return bytes, nil
}

// Count answers how many entries are stored, for the eviction loop to know
// when there is nothing left to evict.
func (c Cache) Count(ctx context.Context) (int64, error) {
	count, err := metadatadb.New(c.store.Read()).CountMetadataCache(ctx)
	if err != nil {
		return 0, fmt.Errorf("metacache: count: %w", err)
	}
	return count, nil
}

// DeleteOlderThan removes every entry fetched before cutoff (ADR-60's
// max_age, a per-entry ceiling enforced regardless of budget). Returns how
// many were removed.
func (c Cache) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	var removed int64
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		removed, err = metadatadb.New(tx).DeleteMetadataCacheOlderThan(ctx, cutoff.Unix())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("metacache: delete older than %s: %w", cutoff, err)
	}
	return removed, nil
}

// DeleteOldest removes up to n entries, oldest first (ADR-60's budget-
// triggered eviction). Returns how many were actually removed, which may be
// fewer than n if the cache holds less.
func (c Cache) DeleteOldest(ctx context.Context, n int64) (int64, error) {
	var removed int64
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		removed, err = metadatadb.New(tx).DeleteOldestMetadataCache(ctx, n)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("metacache: delete oldest %d: %w", n, err)
	}
	return removed, nil
}

// Clear removes every entry (Invariant 5: deleting the cache is always
// safe -- a miss just costs a re-fetch, never data).
func (c Cache) Clear(ctx context.Context) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return metadatadb.New(tx).ClearMetadataCache(ctx)
	})
	if err != nil {
		return fmt.Errorf("metacache: clear: %w", err)
	}
	return nil
}
