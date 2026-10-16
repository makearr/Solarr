package app

import (
	"context"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
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
func (c *indexerCapsCache) musicParams(ctx context.Context, indexerID int64, baseURL string, client indexer.Client) []string {
	return c.searchParams(ctx, indexerID, baseURL, client, "music-search")
}

// movieParams is musicParams for the movie search function.
func (c *indexerCapsCache) movieParams(ctx context.Context, indexerID int64, baseURL string, client indexer.Client) []string {
	return c.searchParams(ctx, indexerID, baseURL, client, "movie-search")
}

// tvParams is musicParams for the TV search function.
func (c *indexerCapsCache) tvParams(ctx context.Context, indexerID int64, baseURL string, client indexer.Client) []string {
	return c.searchParams(ctx, indexerID, baseURL, client, "tv-search")
}

func (c *indexerCapsCache) searchParams(ctx context.Context, indexerID int64, baseURL string, client indexer.Client, search string) []string {
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
