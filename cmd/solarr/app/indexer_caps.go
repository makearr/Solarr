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
	entries map[int64]indexerCapsEntry
}

func newIndexerCapsCache(cl clock.Clock) *indexerCapsCache {
	return &indexerCapsCache{clock: cl, entries: map[int64]indexerCapsEntry{}}
}

// musicParams answers the cached parameters, asking the indexer when they are missing, stale or
// for another address; nil when it cannot say, so the search sends everything as before.
func (c *indexerCapsCache) musicParams(ctx context.Context, indexerID int64, baseURL string, client indexer.Client) []string {
	now := c.clock.Now()
	c.mu.Lock()
	entry, found := c.entries[indexerID]
	c.mu.Unlock()
	if found && entry.baseURL == baseURL && now.Sub(entry.fetchedAt) < indexerCapsTTL {
		return entry.params
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return nil
	}
	music, ok := caps.Search["music-search"]
	if !ok || !music.Available {
		return nil
	}
	params := music.SupportedParams
	if params == nil {
		params = []string{}
	}
	c.mu.Lock()
	c.entries[indexerID] = indexerCapsEntry{baseURL: baseURL, params: params, fetchedAt: now}
	c.mu.Unlock()
	return params
}
