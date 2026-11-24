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

	mu        sync.Mutex
	providers map[string]*providerBudget
}

type providerBudget struct {
	limits Limits

	lastRequest         time.Time
	pausedUntil         time.Time
	consecutiveFailures int

	slots    *gate
	inFlight int

	// waiting counts callers blocked in Acquire per priority, so a token goes to the most urgent.
	waiting map[Priority]int
	turns   laneTurns
}

// NewLimiter builds the registry. jitter randomises computed backoff; pass nil
// for full jitter, or a deterministic function in tests (ADR-17).
func NewLimiter(c clock.Clock, config Config, jitter func(time.Duration) time.Duration) *Limiter {
	if jitter == nil {
		jitter = fullJitter
	}
	return &Limiter{
		clock:     c,
		jitter:    jitter,
		config:    config,
		providers: make(map[string]*providerBudget),
	}
}

// Acquire blocks until the provider may be called, then returns a release function the caller
// must invoke when the request finishes. A due token goes to the lane whose turn it is, then to the most urgent waiter in it (PriorityFrom).
func (l *Limiter) Acquire(ctx context.Context, provider string) (release func(), err error) {
	priority := PriorityFrom(ctx)
	// The slot comes before the pacing turn: requests queued behind a slow one would otherwise all start at once when it ends.
	budget := l.budgetFor(provider)
	if err := budget.slots.acquire(ctx, priority); err != nil {
		return nil, err
	}
	l.wait(provider, priority, 1)
	for {
		wait, err := l.tryTake(provider, priority)
		if err == nil && wait > 0 {
			err = l.clock.Sleep(ctx, wait)
		}
		if err != nil {
			l.wait(provider, priority, -1)
			budget.slots.release()
			return nil, err
		}
		if wait == 0 {
			break
		}
	}
	// Holding its turn, the request no longer keeps its lane's turn from the others.
	l.wait(provider, priority, -1)

	l.mu.Lock()
	budget.inFlight++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			budget.inFlight--
			l.mu.Unlock()
			budget.slots.release()
		})
	}, nil
}

// maxRetryAfter caps a server-chosen pause: one bad answer (a year-long Retry-After) must not disable a provider until restart.
const maxRetryAfter = 6 * time.Hour

// maxBlockingPause is the longest a request waits out a provider's pause; a longer one (a spent quota's Retry-After)
// fails the request instead, so a scan is not held for hours by an enrichment source.
const maxBlockingPause = 2 * time.Minute

// PausedError reports a provider asked to be left alone until a time too far off to wait for.
type PausedError struct {
	Provider string
	Until    time.Time
}

func (e *PausedError) Error() string {
	return fmt.Sprintf("httpx: %s paused until %s", e.Provider, e.Until.Format(time.RFC3339))
}

func (e *PausedError) Unwrap() error { return ErrCircuitOpen }

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

// tryTake takes the provider's next turn if it is due and no more urgent caller is waiting for it. It returns
// how long to wait otherwise, so the caller sleeps outside the lock.
func (l *Limiter) tryTake(provider string, priority Priority) (time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	budget := l.budgetForLocked(provider)
	now := l.clock.Now()

	if budget.pausedUntil.After(now) {
		if pause := budget.pausedUntil.Sub(now); pause > maxBlockingPause {
			return 0, &PausedError{Provider: provider, Until: budget.pausedUntil}
		}
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

	mine := laneOf(priority)
	var waiting [laneCount]bool
	waiting[mine] = true
	for queued := range budget.waiting {
		waiting[laneOf(queued)] = true
		if laneOf(queued) == mine && queued < priority {
			return laneRecheck, nil
		}
	}
	shares := sharesFunc(l.config.LaneShares).read()
	if turn, _ := budget.turns.next(shares, waiting); turn != mine {
		return laneRecheck, nil
	}
	budget.turns.took(shares, waiting, mine)

	budget.lastRequest = now
	return 0, nil
}

// Observe records how a request ended: a failure pauses the provider, a success ends the run of failures.
func (l *Limiter) Observe(provider string, outcome Outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()

	budget := l.budgetForLocked(provider)
	now := l.clock.Now()

	if outcome.Success {
		budget.consecutiveFailures = 0
		return
	}

	budget.consecutiveFailures++

	pause := min(outcome.RetryAfter, maxRetryAfter)
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
	return ProviderState{
		Provider:            provider,
		PausedUntil:         budget.pausedUntil,
		ConsecutiveFailures: budget.consecutiveFailures,
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

	budget := &providerBudget{
		limits:  limits,
		slots:   newGate(concurrent, sharesFunc(l.config.LaneShares)),
		waiting: make(map[Priority]int),
	}
	l.providers[provider] = budget
	return budget
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
