package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// indexerCapsTTL is how long an indexer's music-search parameters are trusted before t=caps is
// asked again; one request a day per indexer.
const indexerCapsTTL = 24 * time.Hour

type capsKey struct {
	indexerID int64
	search    string
}

type indexerCapsEntry struct {
	baseURL   string
	params    []string
	fetchedAt time.Time
}

// indexerCapsCache remembers what each indexer's music search accepts, so a search skips the
// structured query an indexer cannot answer (2026-09-29).
type indexerCapsCache struct {
	mu      sync.Mutex
	clock   clock.Clock
	entries map[capsKey]indexerCapsEntry
}

func newIndexerCapsCache(cl clock.Clock) *indexerCapsCache {
	return &indexerCapsCache{clock: cl, entries: map[capsKey]indexerCapsEntry{}}
}

// musicParams answers the cached parameters, asking the indexer when they are missing, stale or
// for another address; nil when it cannot say, so the search sends everything as before.
func (c *indexerCapsCache) musicParams(ctx context.Context, indexerID int64, baseURL string, client indexer.Driver) []string {
	return c.searchParams(ctx, indexerID, baseURL, client, "music-search")
}

func (c *indexerCapsCache) searchParams(ctx context.Context, indexerID int64, baseURL string, client indexer.Driver, search string) []string {
	now := c.clock.Now()
	key := capsKey{indexerID, search}
	c.mu.Lock()
	entry, found := c.entries[key]
	c.mu.Unlock()
	if found && entry.baseURL == baseURL && now.Sub(entry.fetchedAt) < indexerCapsTTL {
		return entry.params
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return nil
	}
	function, ok := caps.Search[search]
	if !ok || !function.Available {
		return nil
	}
	params := function.SupportedParams
	if params == nil {
		params = []string{}
	}
	c.mu.Lock()
	c.entries[key] = indexerCapsEntry{baseURL: baseURL, params: params, fetchedAt: now}
	c.mu.Unlock()
	return params
}

// capsRefreshAfter is how old the stored categories of an indexer get before they are read again.
const capsRefreshAfter = 7 * 24 * time.Hour

// categoriesFor answers the categories to search an indexer in for the media type rooted at root, from the capabilities
// stored on it, read from the indexer first when none are stored or they are old. ask is false when the indexer lists
// nothing for the type, or cannot be read: it is not worth a request. An indexer that lists no categories at all is
// asked without any.
func (p *process) categoriesFor(ctx context.Context, found settings.Indexer, client indexer.Driver, root int) (ids []int, ask bool) {
	listed, readable := p.storedCategories(ctx, found, client)
	if !readable {
		return nil, false
	}
	if len(listed) == 0 {
		return nil, true
	}
	ids = indexer.CategoriesIn(listed, root)
	return ids, len(ids) > 0
}

// storedCategories reads the categories kept on the indexer, refreshing them through its t=caps when missing or old.
func (p *process) storedCategories(ctx context.Context, found settings.Indexer, client indexer.Driver) ([]indexer.Category, bool) {
	now := clock.NewSystem().Now()
	stale := found.CategoriesJSON == "" || found.CapsFetchedAt == nil || now.Sub(time.Unix(*found.CapsFetchedAt, 0)) > capsRefreshAfter
	if stale {
		if caps, err := client.Capabilities(ctx); err == nil {
			if encoded, err := json.Marshal(caps); err == nil {
				if err := p.cache.SetIndexerCaps(ctx, found.ID, string(encoded), now.Unix()); err != nil {
					p.logger.Warn("indexer capabilities not stored", slog.Int64("indexer_id", found.ID), slog.Any("error", err))
				}
			}
			return caps.Categories, true
		}
	}
	if found.CategoriesJSON == "" {
		return nil, false
	}
	var caps indexer.Capabilities
	if err := json.Unmarshal([]byte(found.CategoriesJSON), &caps); err != nil {
		return nil, false
	}
	return caps.Categories, true
}
