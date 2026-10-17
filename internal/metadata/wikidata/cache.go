package wikidata

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Cached answers what a lookup asks of Wikidata from what an earlier bulk read kept: a refresh sweep warms it with every
// work it is about to read (a few queries for all of them), and each work's lookup then costs nothing. What it lacks it
// reads, one work at a time, and keeps too; a work Wikidata does not know is kept as such.
type Cached struct {
	fetcher Fetcher
	clock   clock.Clock
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]cachedFacts
}

type cachedFacts struct {
	facts WorkFacts
	known bool
	at    time.Time
}

// cachedFactsLimit bounds what is kept; past it, the oldest half goes.
const cachedFactsLimit = 20000

// NewCached keeps what fetcher reads for ttl.
func NewCached(fetcher Fetcher, paced clock.Clock, ttl time.Duration) *Cached {
	return &Cached{fetcher: fetcher, clock: paced, ttl: ttl, entries: map[string]cachedFacts{}}
}

func cacheKey(kind FactKind, id string) string { return string(kind) + ":" + id }

// FactsOf answers each work's facts, reading in bulk only those not kept or kept too long.
func (c *Cached) FactsOf(ctx context.Context, kind FactKind, ids []string) (map[string]WorkFacts, error) {
	now := c.clock.Now()
	out := map[string]WorkFacts{}
	var missing []string
	c.mu.Lock()
	for _, id := range ids {
		entry, kept := c.entries[cacheKey(kind, id)]
		switch {
		case !kept || now.Sub(entry.at) > c.ttl:
			missing = append(missing, id)
		case entry.known:
			out[id] = entry.facts
		}
	}
	c.mu.Unlock()
	if len(missing) == 0 {
		return out, nil
	}
	read, err := c.fetcher.FactsOf(ctx, kind, missing)
	if err != nil {
		return out, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries)+len(missing) > cachedFactsLimit {
		c.forgetOldest()
	}
	for _, id := range missing {
		facts, known := read[id]
		c.entries[cacheKey(kind, id)] = cachedFacts{facts: facts, known: known, at: now}
		if known {
			out[id] = facts
		}
	}
	return out, nil
}

// Warm reads every work not kept yet, in bulk, so the lookups that follow find them.
func (c *Cached) Warm(ctx context.Context, kind FactKind, ids []string) error {
	_, err := c.FactsOf(ctx, kind, ids)
	return err
}

// SeriesIDs answers from the series' facts when TheTVDB's id is known, else asks Wikidata by TMDB's or IMDb's.
func (c *Cached) SeriesIDs(ctx context.Context, known WorkIDs) (WorkIDs, bool, error) {
	if known.TVDB <= 0 {
		return c.fetcher.SeriesIDs(ctx, known)
	}
	id := strconv.FormatInt(known.TVDB, 10)
	facts, err := c.FactsOf(ctx, FactSeries, []string{id})
	found, ok := facts[id]
	return found.IDs, ok && err == nil, err
}

// MovieIDs answers from the film's facts when TMDB's id is known, else asks Wikidata by IMDb's.
func (c *Cached) MovieIDs(ctx context.Context, tmdb int64, imdb string) (WorkIDs, bool, error) {
	if tmdb <= 0 {
		return c.fetcher.MovieIDs(ctx, tmdb, imdb)
	}
	id := strconv.FormatInt(tmdb, 10)
	facts, err := c.FactsOf(ctx, FactFilm, []string{id})
	found, ok := facts[id]
	return found.IDs, ok && err == nil, err
}

// forgetOldest drops the older half of what is kept; called with the lock held.
func (c *Cached) forgetOldest() {
	times := make([]time.Time, 0, len(c.entries))
	for _, entry := range c.entries {
		times = append(times, entry.at)
	}
	if len(times) == 0 {
		return
	}
	slices.SortFunc(times, time.Time.Compare)
	middle := times[len(times)/2]
	for key, entry := range c.entries {
		if !entry.at.After(middle) {
			delete(c.entries, key)
		}
	}
}
