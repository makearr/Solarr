package httpx

import (
	"errors"
	"time"
)

var (
	// ErrDailyCapReached reports a provider whose hard daily quota is spent. It
	// is not waited out: the reset can be most of a day away, and blocking a
	// worker that long is worse than reporting the provider unavailable.
	ErrDailyCapReached = errors.New("httpx: provider daily cap reached")
)

// Limits is one provider's budget. A zero field means the registry default
// applies; a zero default means unlimited.
type Limits struct {
	RequestsPerMinute int
	MaxConcurrent     int
	// MinInterval is a floor between consecutive requests, independent of the
	// bucket. MusicBrainz allows one request per second and means it (ADR-29).
	MinInterval time.Duration
	// DailyCap is a hard quota for API-limited accounts. Zero means none.
	DailyCap int
}

// Config is the whole outbound budget: one global concurrency cap over
// per-provider buckets (ADR-13).
type Config struct {
	GlobalMaxConcurrent int
	Defaults            Limits
	Providers           map[string]Limits
}

// backoffBase is the first pause after a failure with no Retry-After.
const backoffBase = 2 * time.Second

// backoffCeiling caps exponential growth. Beyond this the circuit breaker is the
// right mechanism, not a longer sleep.
const backoffCeiling = 5 * time.Minute

// recoverySteps is how many consecutive successes return a halved provider to
// its full configured rate. Recovery is additive and never snaps back (ADR-13).
const recoverySteps = 10

// minimumRateFraction floors the halving, so a provider always retains enough
// rate to prove it has recovered.
const minimumRateFraction = 0.05

// resolve fills zero fields from the defaults.
func (l Limits) resolve(defaults Limits) Limits {
	if l.RequestsPerMinute == 0 {
		l.RequestsPerMinute = defaults.RequestsPerMinute
	}
	if l.MaxConcurrent == 0 {
		l.MaxConcurrent = defaults.MaxConcurrent
	}
	if l.MinInterval == 0 {
		l.MinInterval = defaults.MinInterval
	}
	if l.DailyCap == 0 {
		l.DailyCap = defaults.DailyCap
	}
	return l
}

// Outcome is what a completed request tells the limiter. Searches, RSS polls and
// metadata lookups all report through here, which is what makes them share one
// provider's budget rather than three parallel ones.
type Outcome struct {
	Success bool
	// RetryAfter is the header's value when the provider sent one. It is
	// honoured verbatim, in preference to any computed backoff.
	RetryAfter time.Duration
}

// ProviderState is what the user is shown when work is paused (Invariant 29): a
// provider quietly returning nothing is indistinguishable from a library with
// nothing to find.
type ProviderState struct {
	Provider string
	// Zero in both means the provider is unrated, not that it is stopped.
	ConfiguredPerMinute float64
	EffectivePerMinute  float64
	PausedUntil         time.Time
	ConsecutiveFailures int
	DailyUsed           int
	DailyCap            int
	InFlight            int
}

// Throttled reports whether the provider is running below its configured rate or
// is currently paused.
func (s ProviderState) Throttled(now time.Time) bool {
	return s.PausedUntil.After(now) || s.EffectivePerMinute < s.ConfiguredPerMinute
}
