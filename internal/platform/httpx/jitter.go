package httpx

import (
	"math/rand/v2"
	"time"
)

// fullJitter spreads a computed backoff over [0, d] (ADR-17).
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d)) + 1)
}

// NoJitter returns the backoff unchanged. Exported for tests and for the
// deterministic scheduler mode.
func NoJitter(d time.Duration) time.Duration { return d }
