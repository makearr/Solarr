package app

import (
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// indexerBasePace is the gap httpx keeps between requests to an indexer nobody set a limit for.
const indexerBasePace = 500 * time.Millisecond

// indexerLimitMargin stays this fraction under an indexer's stated limit.
const indexerLimitMargin = 0.8

// indexerSlowestFactor caps how far a limit hit slows an indexer down.
const indexerSlowestFactor = 16

// indexerPacing is the pace each indexer is asked at: the limit its owner or Prowlarr stated, kept under by a margin,
// and slowed further (doubling each time) while the indexer answers "limit reached", easing off by half every hour
// without one.
type indexerPacing struct {
	mutex      sync.Mutex
	clock      clock.Clock
	configured map[int64]time.Duration
	floors     map[int64]time.Duration
	hits       map[int64]limitHit
}

type limitHit struct {
	factor float64
	at     time.Time
}

func newIndexerPacing(cl clock.Clock) *indexerPacing {
	return &indexerPacing{clock: cl, configured: map[int64]time.Duration{}, floors: map[int64]time.Duration{}, hits: map[int64]limitHit{}}
}

// setLimit records the queries an hour an indexer allows; zero clears it.
func (x *indexerPacing) setLimit(id, queriesPerHour int64) {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	if queriesPerHour <= 0 {
		delete(x.configured, id)
		return
	}
	x.configured[id] = time.Duration(float64(time.Hour) / (float64(queriesPerHour) * indexerLimitMargin))
}

// setFloor records the least gap a site asks between requests (a spec's request delay); zero clears it.
func (x *indexerPacing) setFloor(id int64, gap time.Duration) {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	if gap <= 0 {
		delete(x.floors, id)
		return
	}
	x.floors[id] = gap
}

// limitHit notes that the indexer answered "limit reached".
func (x *indexerPacing) limitHit(id int64) {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	now := x.clock.Now()
	factor := x.factorLocked(id, now) * 2
	x.hits[id] = limitHit{factor: min(factor, indexerSlowestFactor), at: now}
}

func (x *indexerPacing) factorLocked(id int64, now time.Time) float64 {
	hit, ok := x.hits[id]
	if !ok {
		return 1
	}
	factor := hit.factor
	for halved := now.Sub(hit.at); halved >= time.Hour && factor > 1; halved -= time.Hour {
		factor /= 2
	}
	return max(factor, 1)
}

// interval is the gap to keep between requests to the indexer; false when nothing sets it apart from the default.
func (x *indexerPacing) interval(id int64) (time.Duration, bool) {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	factor := x.factorLocked(id, x.clock.Now())
	base, limited := x.configured[id]
	floor, floored := x.floors[id]
	if !limited && !floored && factor <= 1 {
		return 0, false
	}
	if !limited {
		base = indexerBasePace
	}
	return time.Duration(float64(max(base, floor)) * factor), true
}
