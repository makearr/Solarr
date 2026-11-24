package httpx

import (
	"strings"
	"time"
)

// Limits is one provider's budget. A zero field means the registry default
// applies; a zero default means unlimited.
type Limits struct {
	MaxConcurrent int
	// MinInterval is the floor between consecutive requests. MusicBrainz allows one request per second and means it (ADR-29).
	MinInterval time.Duration
}

// Config is the whole outbound budget: per-provider pacing and concurrency (ADR-13).
type Config struct {
	Defaults  Limits
	Providers map[string]Limits
	// Prefixes are the limits of a family of providers named "<prefix><id>", one bucket each: "indexer:7".
	Prefixes map[string]Limits
	// LivePace answers a provider's MinInterval as a person set it, read at every request so a change applies at once;
	// false keeps the declared one.
	LivePace func(provider string) (time.Duration, bool)
	// LaneShares answers the user, fresh and background lanes' weights, read at every decision; nil is DefaultLaneShares.
	LaneShares func() LaneShares
}

// limitsFor is a provider's own limits, else its family's (longest prefix), else none; defaults fill the gaps.
func (c Config) limitsFor(provider string) Limits {
	limits, named := c.Providers[provider]
	if !named {
		longest := -1
		for prefix, family := range c.Prefixes {
			if strings.HasPrefix(provider, prefix) && len(prefix) > longest {
				limits, longest = family, len(prefix)
			}
		}
	}
	return limits.resolve(c.Defaults)
}

// backoffBase is the first pause after a failure with no Retry-After.
const backoffBase = 2 * time.Second

// backoffCeiling caps exponential growth. Beyond this the circuit breaker is the
// right mechanism, not a longer sleep.
const backoffCeiling = 5 * time.Minute

// resolve fills zero fields from the defaults.
func (l Limits) resolve(defaults Limits) Limits {
	if l.MaxConcurrent == 0 {
		l.MaxConcurrent = defaults.MaxConcurrent
	}
	if l.MinInterval == 0 {
		l.MinInterval = defaults.MinInterval
	}
	return l
}

// Outcome is what a completed request tells the limiter.
type Outcome struct {
	Success bool
	// RetryAfter is the header's value when the provider sent one. It is
	// honoured verbatim, in preference to any computed backoff.
	RetryAfter time.Duration
}

// ProviderState is what the user is shown when work is paused (Invariant 29): a provider
// quietly returning nothing is indistinguishable from a library with nothing to find.
type ProviderState struct {
	Provider            string
	PausedUntil         time.Time
	ConsecutiveFailures int
	InFlight            int
}

// Throttled reports whether the provider is currently paused.
func (s ProviderState) Throttled(now time.Time) bool { return s.PausedUntil.After(now) }
