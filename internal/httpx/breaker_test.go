package httpx_test

import (
	"errors"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/httpx"
)

func newBreaker(config httpx.BreakerConfig) (*httpx.Breaker, *clock.Fake) {
	fake := clock.NewFakeAtEpoch()
	return httpx.NewBreaker(fake, config), fake
}

func TestBreaker_StartsClosedAndStaysClosedBelowTheThreshold(t *testing.T) {
	breaker, _ := newBreaker(httpx.BreakerConfig{FailureThreshold: 3, Cooldown: time.Minute})

	for failure := range 2 {
		if err := breaker.Allow("indexer"); err != nil {
			t.Fatalf("Allow after %d failures: %v", failure, err)
		}
		breaker.Observe("indexer", false)
	}
	if got := breaker.State("indexer").State; got != httpx.BreakerClosed {
		t.Errorf("State = %q after 2 of 3 failures, want closed", got)
	}
}

func TestBreaker_OpensAtTheThresholdAndSkipsTheProvider(t *testing.T) {
	breaker, fake := newBreaker(httpx.BreakerConfig{FailureThreshold: 3, Cooldown: time.Minute})

	for range 3 {
		_ = breaker.Allow("indexer")
		breaker.Observe("indexer", false)
	}

	err := breaker.Allow("indexer")
	if !errors.Is(err, httpx.ErrCircuitOpen) {
		t.Fatalf("Allow = %v, want ErrCircuitOpen", err)
	}

	// The user has to be told what is paused and until when (Invariant 29).
	var open *httpx.OpenError
	if !errors.As(err, &open) {
		t.Fatalf("error %v carries no OpenError", err)
	}
	if want := fake.Now().Add(time.Minute); !open.RetryAt.Equal(want) {
		t.Errorf("RetryAt = %v, want %v", open.RetryAt, want)
	}
}

func TestBreaker_SuccessResetsTheFailureRun(t *testing.T) {
	breaker, _ := newBreaker(httpx.BreakerConfig{FailureThreshold: 3, Cooldown: time.Minute})

	breaker.Observe("indexer", false)
	breaker.Observe("indexer", false)
	breaker.Observe("indexer", true)
	breaker.Observe("indexer", false)
	breaker.Observe("indexer", false)

	// Four failures in total, but never three consecutively.
	if err := breaker.Allow("indexer"); err != nil {
		t.Errorf("Allow = %v; non-consecutive failures must not open the circuit", err)
	}
}

func TestBreaker_ProbesOnceAfterTheCooldown(t *testing.T) {
	breaker, fake := newBreaker(httpx.BreakerConfig{FailureThreshold: 1, Cooldown: time.Minute})

	breaker.Observe("indexer", false)
	if err := breaker.Allow("indexer"); !errors.Is(err, httpx.ErrCircuitOpen) {
		t.Fatalf("Allow before the cooldown = %v, want ErrCircuitOpen", err)
	}

	fake.Advance(time.Minute)
	if err := breaker.Allow("indexer"); err != nil {
		t.Fatalf("Allow after the cooldown = %v, want the probe to be let through", err)
	}
	if got := breaker.State("indexer").State; got != httpx.BreakerHalfOpen {
		t.Errorf("State = %q, want half_open", got)
	}

	// Exactly one probe: a backlog of waiting callers must not all hit a
	// recovering provider at once.
	if err := breaker.Allow("indexer"); !errors.Is(err, httpx.ErrCircuitOpen) {
		t.Errorf("a second concurrent probe was allowed: %v", err)
	}
}

func TestBreaker_SuccessfulProbeCloses(t *testing.T) {
	breaker, fake := newBreaker(httpx.BreakerConfig{FailureThreshold: 1, Cooldown: time.Minute})

	breaker.Observe("indexer", false)
	fake.Advance(time.Minute)
	if err := breaker.Allow("indexer"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	breaker.Observe("indexer", true)

	if got := breaker.State("indexer").State; got != httpx.BreakerClosed {
		t.Errorf("State = %q after a successful probe, want closed", got)
	}
	if err := breaker.Allow("indexer"); err != nil {
		t.Errorf("Allow after recovery = %v, want nil", err)
	}
}

func TestBreaker_FailedProbeReopensWithALongerCooldown(t *testing.T) {
	breaker, fake := newBreaker(httpx.BreakerConfig{
		FailureThreshold: 1, Cooldown: time.Minute, MaxCooldown: time.Hour,
	})

	breaker.Observe("indexer", false)
	fake.Advance(time.Minute)
	_ = breaker.Allow("indexer")
	breaker.Observe("indexer", false) // the probe failed

	if got := breaker.State("indexer").State; got != httpx.BreakerOpen {
		t.Fatalf("State = %q after a failed probe, want open", got)
	}

	// One attempt is not evidence of recovery, so the next wait is longer.
	fake.Advance(time.Minute)
	if err := breaker.Allow("indexer"); !errors.Is(err, httpx.ErrCircuitOpen) {
		t.Errorf("Allow after the original cooldown = %v; the cooldown did not grow", err)
	}
	fake.Advance(time.Minute)
	if err := breaker.Allow("indexer"); err != nil {
		t.Errorf("Allow after the doubled cooldown = %v, want the probe through", err)
	}
}

func TestBreaker_CooldownIsCapped(t *testing.T) {
	breaker, fake := newBreaker(httpx.BreakerConfig{
		FailureThreshold: 1, Cooldown: time.Minute, MaxCooldown: 4 * time.Minute,
	})

	breaker.Observe("indexer", false)
	for range 10 {
		fake.Advance(time.Hour)
		_ = breaker.Allow("indexer")
		breaker.Observe("indexer", false)
	}

	snapshot := breaker.State("indexer")
	if got := snapshot.RetryAt.Sub(snapshot.OpenedAt); got != 4*time.Minute {
		t.Errorf("cooldown = %v, want it capped at 4m", got)
	}
}

func TestBreaker_OneProviderDoesNotAffectAnother(t *testing.T) {
	breaker, _ := newBreaker(httpx.BreakerConfig{FailureThreshold: 1, Cooldown: time.Minute})

	breaker.Observe("dead-indexer", false)
	if err := breaker.Allow("dead-indexer"); !errors.Is(err, httpx.ErrCircuitOpen) {
		t.Fatalf("dead indexer = %v, want ErrCircuitOpen", err)
	}
	// Invariant 27: one failing provider never stops the others.
	if err := breaker.Allow("healthy-indexer"); err != nil {
		t.Errorf("healthy indexer = %v, want nil", err)
	}
}
