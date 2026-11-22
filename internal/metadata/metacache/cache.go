// Package metacache is the permanent, generic metadata cache behind cache/metadata.db (ADR-2, ADR-29): kind and external id are opaque and the body is caller JSON, so every provider shares it; it is disposable (no backup rotation, a miss costs a re-fetch).
package metacache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/metadatadb"
	"github.com/makearr/Solarr/internal/platform/hitcount"
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
type Cache struct {
	store *store.Store
	// Lookups counts what Get answered from the cache; optional.
	Lookups *hitcount.Counter
}

// New builds it over an open metadata store.
func New(opened *store.Store) Cache { return Cache{store: opened} }

// Counting answers the cache with its lookups counted into counter.
func (c Cache) Counting(counter *hitcount.Counter) Cache {
	c.Lookups = counter
	return c
}

// Get returns the cached entry for (kind, externalID), or ErrNotFound.
func (c Cache) Get(ctx context.Context, kind, externalID string) (Entry, error) {
	row, err := metadatadb.New(c.store.Read()).GetCachedMetadata(ctx, metadatadb.GetCachedMetadataParams{
		Kind: kind, ExternalID: externalID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		c.Lookups.Miss()
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("metacache: get %s:%s: %w", kind, externalID, err)
	}
	c.Lookups.Hit()
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

// SizeBytes answers the logical bytes stored (ADR-60), not the physical file size, which SQLite does not shrink on DELETE without a VACUUM this codebase does not run.
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

// expiryChunk is how many expired entries go in one write transaction.
const expiryChunk = 200

// DeleteOlderThan removes every entry fetched before cutoff (ADR-60's
// max_age, a per-entry ceiling enforced regardless of budget), a chunk per
// transaction. Returns how many were removed.
func (c Cache) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var removed int64
		err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			removed, err = metadatadb.New(tx).DeleteMetadataCacheOlderThan(ctx, metadatadb.DeleteMetadataCacheOlderThanParams{
				FetchedAt: cutoff.Unix(), Limit: expiryChunk,
			})
			return err
		})
		if err != nil {
			return total, fmt.Errorf("metacache: delete older than %s: %w", cutoff, err)
		}
		total += removed
		if removed < expiryChunk {
			return total, nil
		}
	}
}

// DeleteOldest removes up to n entries, oldest first (ADR-60's budget-triggered eviction): how many went, which may
// be fewer than n, and the bytes they held as SizeBytes counts them.
func (c Cache) DeleteOldest(ctx context.Context, n int64) (removed, bytes int64, err error) {
	err = c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		sizes, err := metadatadb.New(tx).DeleteOldestMetadataCache(ctx, n)
		removed, bytes = int64(len(sizes)), 0
		for _, size := range sizes {
			bytes += size
		}
		return err
	})
	if err != nil {
		return 0, 0, fmt.Errorf("metacache: delete oldest %d: %w", n, err)
	}
	return removed, bytes, nil
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
