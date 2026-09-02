package httpx

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
)

// ErrCircuitOpen reports a provider being skipped entirely (Invariant 27).
var ErrCircuitOpen = errors.New("httpx: circuit open")

// BreakerState is where a provider sits in the open/probe/closed cycle.
type BreakerState string

const (
	// BreakerClosed is normal operation.
	BreakerClosed BreakerState = "closed"
	// BreakerOpen skips the provider entirely for a cooldown.
	BreakerOpen BreakerState = "open"
	// BreakerHalfOpen allows exactly one probe request through.
	BreakerHalfOpen BreakerState = "half_open"
)

// BreakerConfig tunes when a provider is given up on. These are expert-tier
// settings (ADR-26); the defaults work untouched.
type BreakerConfig struct {
	// FailureThreshold is how many consecutive failures open the circuit.
	FailureThreshold int
	// Cooldown is the first pause before a probe. It doubles on each failed
	// probe, up to MaxCooldown.
	Cooldown    time.Duration
	MaxCooldown time.Duration
}

func (c BreakerConfig) withDefaults() BreakerConfig {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 5
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 30 * time.Second
	}
	if c.MaxCooldown <= 0 {
		c.MaxCooldown = 15 * time.Minute
	}
	return c
}

// OpenError reports a skipped provider and when it will next be probed, so the
// user can be told what is paused and until when (Invariant 29).
type OpenError struct {
	Provider  string
	RetryAt   time.Time
	OpenSince time.Time
}

func (e *OpenError) Error() string {
	return fmt.Sprintf("httpx: %s circuit open since %s, probing at %s",
		e.Provider, e.OpenSince.Format(time.RFC3339), e.RetryAt.Format(time.RFC3339))
}

func (e *OpenError) Unwrap() error { return ErrCircuitOpen }

// BreakerSnapshot is one provider's breaker state, for the UI and notifications.
type BreakerSnapshot struct {
	Provider            string
	State               BreakerState
	ConsecutiveFailures int
	OpenedAt            time.Time
	RetryAt             time.Time
}

// Breaker skips providers that are consistently failing (ADR-13 step 3, Invariant 26).
type Breaker struct {
	clock  clock.Clock
	config BreakerConfig

	mu        sync.Mutex
	providers map[string]*breakerEntry
}

type breakerEntry struct {
	state               BreakerState
	consecutiveFailures int
	openedAt            time.Time
	cooldown            time.Duration
	// probing marks the single in-flight half-open request.
	probing bool
}

// NewBreaker returns a breaker. A zero config takes the defaults.
func NewBreaker(c clock.Clock, config BreakerConfig) *Breaker {
	return &Breaker{
		clock:     c,
		config:    config.withDefaults(),
		providers: make(map[string]*breakerEntry),
	}
}

// Allow reports whether a request to provider may proceed.
func (b *Breaker) Allow(provider string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(provider)
	now := b.clock.Now()

	switch entry.state {
	case BreakerClosed:
		return nil

	case BreakerOpen:
		retryAt := entry.openedAt.Add(entry.cooldown)
		if now.Before(retryAt) {
			return &OpenError{Provider: provider, RetryAt: retryAt, OpenSince: entry.openedAt}
		}
		entry.state = BreakerHalfOpen
		entry.probing = true
		return nil

	case BreakerHalfOpen:
		if entry.probing {
			return &OpenError{
				Provider:  provider,
				RetryAt:   entry.openedAt.Add(entry.cooldown),
				OpenSince: entry.openedAt,
			}
		}
		entry.probing = true
		return nil

	default:
		return nil
	}
}

// Observe records the result of a request that Allow permitted.
func (b *Breaker) Observe(provider string, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(provider)
	now := b.clock.Now()

	if success {
		entry.state = BreakerClosed
		entry.consecutiveFailures = 0
		entry.probing = false
		entry.cooldown = b.config.Cooldown
		entry.openedAt = time.Time{}
		return
	}

	entry.consecutiveFailures++
	if entry.state == BreakerHalfOpen {
		// The probe failed, so the provider is still down and the next wait is
		// longer. Recovery is never assumed on a single attempt.
		entry.probing = false
		entry.state = BreakerOpen
		entry.openedAt = now
		entry.cooldown = min(entry.cooldown*2, b.config.MaxCooldown)
		return
	}
	if entry.consecutiveFailures >= b.config.FailureThreshold {
		entry.state = BreakerOpen
		entry.openedAt = now
	}
}

// State reports the provider's breaker state.
func (b *Breaker) State(provider string) BreakerSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(provider)
	snapshot := BreakerSnapshot{
		Provider:            provider,
		State:               entry.state,
		ConsecutiveFailures: entry.consecutiveFailures,
		OpenedAt:            entry.openedAt,
	}
	if entry.state != BreakerClosed {
		snapshot.RetryAt = entry.openedAt.Add(entry.cooldown)
	}
	return snapshot
}

func (b *Breaker) entryLocked(provider string) *breakerEntry {
	if existing, ok := b.providers[provider]; ok {
		return existing
	}
	entry := &breakerEntry{state: BreakerClosed, cooldown: b.config.Cooldown}
	b.providers[provider] = entry
	return entry
}
