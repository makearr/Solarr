// Package hitcount counts how often a cache answered, with two atomic adds and nothing else on the hot path.
package hitcount

import "sync/atomic"

// Counter is a cache's hits and misses since the process started. The zero value and a nil pointer both work, so a cache
// built without one simply does not count.
type Counter struct{ hits, misses atomic.Int64 }

// Hit records a lookup the cache answered.
func (c *Counter) Hit() {
	if c != nil {
		c.hits.Add(1)
	}
}

// Miss records a lookup the cache could not answer.
func (c *Counter) Miss() {
	if c != nil {
		c.misses.Add(1)
	}
}

// Totals answers the hits and misses so far.
func (c *Counter) Totals() (hits, misses int64) {
	if c == nil {
		return 0, 0
	}
	return c.hits.Load(), c.misses.Load()
}
