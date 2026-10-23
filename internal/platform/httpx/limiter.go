package httpx

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Limiter is the one global rate-limit registry (ADR-13).
type Limiter struct {
	clock  clock.Clock
	jitter func(time.Duration) time.Duration
	config Config

	global chan struct{} // global concurrency semaphore

	mu        sync.Mutex
	providers map[string]*providerBudget
}

type providerBudget struct {
	limits Limits

	// tokens and lastRefill are the bucket. Tokens are fractional so a rate
	// below one per second still works.
	tokens     float64
	lastRefill time.Time

	// effectivePerSecond is halved on failure and restored additively, so it is
	// at or below the configured rate but never zero.
	effectivePerSecond float64

	lastRequest         time.Time
	pausedUntil         time.Time
	consecutiveFailures int

	dailyUsed    int
	dailyResetAt time.Time

	slots    chan struct{}
	inFlight int

	// waiting counts callers blocked in Acquire per priority, so a token goes to the most urgent.
	waiting map[Priority]int
}

// NewLimiter builds the registry. jitter randomises computed backoff; pass nil
// for full jitter, or a deterministic function in tests (ADR-17).
func NewLimiter(c clock.Clock, config Config, jitter func(time.Duration) time.Duration) *Limiter {
	if jitter == nil {
		jitter = fullJitter
	}
	global := config.GlobalMaxConcurrent
	if global <= 0 {
		global = math.MaxInt16 // effectively unlimited, still a bounded channel
	}
	return &Limiter{
		clock:     c,
		jitter:    jitter,
		config:    config,
		global:    make(chan struct{}, global),
		providers: make(map[string]*providerBudget),
	}
}

// Acquire blocks until the provider may be called, then returns a release function the caller
// must invoke when the request finishes. A due token goes to the most urgent waiter (PriorityFrom).
func (l *Limiter) Acquire(ctx context.Context, provider string) (release func(), err error) {
	priority := PriorityFrom(ctx)
	l.wait(provider, priority, 1)
	defer l.wait(provider, priority, -1)
	for {
		wait, err := l.tryTake(provider, priority)
		if err != nil {
			return nil, err
		}
		if wait == 0 {
			break
		}
		if err := l.clock.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}

	budget := l.budgetFor(provider)
	if err := acquireSlot(ctx, budget.slots); err != nil {
		l.refund(provider)
		return nil, err
	}
	if err := acquireSlot(ctx, l.global); err != nil {
		releaseSlot(budget.slots)
		l.refund(provider)
		return nil, err
	}

	l.mu.Lock()
	budget.inFlight++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			budget.inFlight--
			l.mu.Unlock()
			releaseSlot(l.global)
			releaseSlot(budget.slots)
		})
	}, nil
}

// laneRecheck is how soon a waiter passed over for a more urgent one looks again.
const laneRecheck = 25 * time.Millisecond

func (l *Limiter) wait(provider string, priority Priority, delta int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	budget := l.budgetForLocked(provider)
	budget.waiting[priority] += delta
	if budget.waiting[priority] <= 0 {
		delete(budget.waiting, priority)
	}
}

// tryTake consumes a token if one is due and no more urgent caller is waiting for it. It returns
// how long to wait otherwise, so the caller sleeps outside the lock.
func (l *Limiter) tryTake(provider string, priority Priority) (time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	budget := l.budgetForLocked(provider)
	now := l.clock.Now()
	budget.rollDailyWindow(now)

	if budget.limits.DailyCap > 0 && budget.dailyUsed >= budget.limits.DailyCap {
		return 0, fmt.Errorf("%w: %s used %d of %d", ErrDailyCapReached, provider, budget.dailyUsed, budget.limits.DailyCap)
	}
	if budget.pausedUntil.After(now) {
		return budget.pausedUntil.Sub(now), nil
	}
	interval := budget.limits.MinInterval
	if l.config.LivePace != nil {
		if pace, own := l.config.LivePace(provider); own {
			interval = pace
		}
	}
	if !budget.lastRequest.IsZero() && interval > 0 {
		if elapsed := now.Sub(budget.lastRequest); elapsed < interval {
			return interval - elapsed, nil
		}
	}

	budget.refill(now)
	if budget.tokens < 1 {
		if budget.effectivePerSecond <= 0 {
			return backoffBase, nil
		}
		missing := 1 - budget.tokens
		return time.Duration(missing / budget.effectivePerSecond * float64(time.Second)), nil
	}

	for waiting := range budget.waiting {
		if waiting < priority {
			return laneRecheck, nil
		}
	}

	budget.tokens--
	budget.lastRequest = now
	budget.dailyUsed++
	return 0, nil
}

// refund returns a token spent for a request that never happened.
func (l *Limiter) refund(provider string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	budget := l.budgetForLocked(provider)
	budget.tokens++
	if budget.dailyUsed > 0 {
		budget.dailyUsed--
	}
}

// Observe records how a request ended, adjusting the provider's effective rate.
func (l *Limiter) Observe(provider string, outcome Outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()

	budget := l.budgetForLocked(provider)
	now := l.clock.Now()

	if outcome.Success {
		budget.consecutiveFailures = 0
		// Additive recovery, never an immediate return to full rate (ADR-13).
		configured := budget.configuredPerSecond()
		budget.effectivePerSecond = math.Min(configured, budget.effectivePerSecond+configured/recoverySteps)
		return
	}

	budget.consecutiveFailures++
	budget.effectivePerSecond = math.Max(
		budget.configuredPerSecond()*minimumRateFraction,
		budget.effectivePerSecond/2,
	)

	pause := outcome.RetryAfter
	if pause <= 0 {
		pause = l.jitter(exponential(budget.consecutiveFailures))
	}
	if until := now.Add(pause); until.After(budget.pausedUntil) {
		budget.pausedUntil = until
	}
}

// State reports what to tell the user about a provider (Invariant 29).
func (l *Limiter) State(provider string) ProviderState {
	l.mu.Lock()
	defer l.mu.Unlock()

	budget := l.budgetForLocked(provider)
	budget.rollDailyWindow(l.clock.Now())

	// An unlimited provider reports zero rather than the sentinel it uses
	// internally, which would otherwise reach the UI as a meaningless number.
	var configuredPerMinute, effectivePerMinute float64
	if budget.limits.RequestsPerMinute > 0 {
		configuredPerMinute = budget.configuredPerSecond() * 60
		effectivePerMinute = budget.effectivePerSecond * 60
	}

	return ProviderState{
		Provider:            provider,
		ConfiguredPerMinute: configuredPerMinute,
		EffectivePerMinute:  effectivePerMinute,
		PausedUntil:         budget.pausedUntil,
		ConsecutiveFailures: budget.consecutiveFailures,
		DailyUsed:           budget.dailyUsed,
		DailyCap:            budget.limits.DailyCap,
		InFlight:            budget.inFlight,
	}
}

func (l *Limiter) budgetFor(provider string) *providerBudget {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.budgetForLocked(provider)
}

func (l *Limiter) budgetForLocked(provider string) *providerBudget {
	if existing, ok := l.providers[provider]; ok {
		return existing
	}

	limits := l.config.limitsFor(provider)
	concurrent := limits.MaxConcurrent
	if concurrent <= 0 {
		concurrent = math.MaxInt16
	}
	now := l.clock.Now()

	budget := &providerBudget{
		limits:       limits,
		lastRefill:   now,
		dailyResetAt: nextUTCMidnight(now),
		slots:        make(chan struct{}, concurrent),
		waiting:      make(map[Priority]int),
	}
	budget.effectivePerSecond = budget.configuredPerSecond()
	// Start full, so the first request of a session is not made to wait.
	budget.tokens = budget.burst()
	l.providers[provider] = budget
	return budget
}

func (b *providerBudget) configuredPerSecond() float64 {
	if b.limits.RequestsPerMinute <= 0 {
		return math.MaxInt16 // unlimited; MinInterval still applies
	}
	return float64(b.limits.RequestsPerMinute) / 60
}

// burst is one minute of budget, capped so an unlimited provider does not
// accumulate a meaningless reserve.
func (b *providerBudget) burst() float64 {
	if b.limits.RequestsPerMinute <= 0 {
		return 1
	}
	return float64(b.limits.RequestsPerMinute)
}

func (b *providerBudget) refill(now time.Time) {
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.lastRefill = now
	b.tokens = math.Min(b.burst(), b.tokens+elapsed*b.effectivePerSecond)
}

// rollDailyWindow resets the quota at UTC midnight, which is when Newznab
// providers conventionally reset theirs.
func (b *providerBudget) rollDailyWindow(now time.Time) {
	if now.Before(b.dailyResetAt) {
		return
	}
	b.dailyUsed = 0
	b.dailyResetAt = nextUTCMidnight(now)
}

func nextUTCMidnight(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
}

func exponential(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	// Shift rather than math.Pow, and clamp before overflowing the exponent.
	if failures > 20 {
		return backoffCeiling
	}
	backoff := backoffBase << (failures - 1)
	if backoff > backoffCeiling {
		return backoffCeiling
	}
	return backoff
}

func acquireSlot(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSlot(slots chan struct{}) {
	select {
	case <-slots:
	default:
	}
}
