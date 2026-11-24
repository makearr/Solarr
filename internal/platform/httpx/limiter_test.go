package httpx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func newLimiter(config httpx.Config) (*httpx.Limiter, *clock.Fake) {
	fake := clock.NewFakeAtEpoch()
	return httpx.NewLimiter(fake, config, httpx.NoJitter), fake
}

func TestLimiter_FirstRequestIsNotDelayed(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		Defaults: httpx.Limits{MinInterval: time.Second},
	})

	release, err := limiter.Acquire(t.Context(), "musicbrainz")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
}

func TestLimiter_MinIntervalSpacesConsecutiveRequests(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		// MusicBrainz: one request per second, and it means it (ADR-29).
		Defaults: httpx.Limits{MinInterval: time.Second},
	})

	release, err := limiter.Acquire(t.Context(), "musicbrainz")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	release()

	acquired := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(t.Context(), "musicbrainz")
		if release != nil {
			release()
		}
		acquired <- err
	}()

	fake.BlockUntilWaiters(1) // the second request is waiting out the interval
	select {
	case err := <-acquired:
		t.Fatalf("second Acquire returned early with %v; the interval was not enforced", err)
	default:
	}

	fake.Advance(time.Second)
	if err := <-acquired; err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
}

// A pace a person sets replaces the declared interval at the next request, without a restart (2026-10-23).
func TestLimiter_LivePaceReplacesTheDeclaredInterval(t *testing.T) {
	pace := 3 * time.Second
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{MinInterval: time.Second},
		LivePace: func(provider string) (time.Duration, bool) { return pace, provider == "tmdb" },
	})
	release, err := limiter.Acquire(t.Context(), "tmdb")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	release()

	acquired := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(t.Context(), "tmdb")
		if release != nil {
			release()
		}
		acquired <- err
	}()
	fake.BlockUntilWaiters(1)
	fake.Advance(time.Second)
	fake.BlockUntilWaiters(1) // the declared second is not enough: it waits out the person's three
	select {
	case err := <-acquired:
		t.Fatalf("second Acquire returned after 1s with %v; the live pace was not used", err)
	default:
	}
	fake.Advance(2 * time.Second)
	if err := <-acquired; err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
}

// The claim ADR-13 exists to make: subsystems calling one provider are paced as one, not each on its own.
func TestLimiter_SearchRSSAndMetadataShareOneProviderPace(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{MinInterval: 20 * time.Second},
	})

	release, err := limiter.Acquire(t.Context(), "my-indexer")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	release()

	blocked := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(t.Context(), "my-indexer")
		if release != nil {
			release()
		}
		blocked <- err
	}()

	fake.BlockUntilWaiters(1)
	select {
	case err := <-blocked:
		t.Fatalf("a second request went through inside the interval (err %v)", err)
	default:
	}

	fake.Advance(20 * time.Second)
	if err := <-blocked; err != nil {
		t.Fatalf("second Acquire after the interval: %v", err)
	}
}

func TestLimiter_SeparateProvidersHaveSeparateBudgets(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		Defaults: httpx.Limits{},
	})

	firstRelease, err := limiter.Acquire(t.Context(), "indexer-a")
	if err != nil {
		t.Fatalf("indexer-a: %v", err)
	}
	firstRelease()

	// indexer-b must not be blocked by indexer-a spending its budget.
	secondRelease, err := limiter.Acquire(t.Context(), "indexer-b")
	if err != nil {
		t.Fatalf("indexer-b: %v", err)
	}
	secondRelease()
}

func TestLimiter_PerProviderConcurrencyCap(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		Defaults: httpx.Limits{MaxConcurrent: 1},
	})

	firstRelease, err := limiter.Acquire(t.Context(), "indexer-a")
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	second := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(t.Context(), "indexer-a")
		if release != nil {
			release()
		}
		second <- err
	}()

	select {
	case err := <-second:
		t.Fatalf("a second concurrent request to the same provider went through with a cap of 1 (err %v)", err)
	case <-time.After(20 * time.Millisecond):
	}

	firstRelease()
	if err := <-second; err != nil {
		t.Fatalf("second Acquire after release: %v", err)
	}
}

func TestLimiter_FailurePauses(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{},
	})

	limiter.Observe("flaky", httpx.Outcome{Success: false})
	after := limiter.State("flaky")
	if !after.PausedUntil.After(fake.Now()) {
		t.Error("a failure did not pause the provider")
	}
	if !after.Throttled(fake.Now()) {
		t.Error("Throttled = false; the user would never be told (Invariant 29)")
	}
}

func TestLimiter_RetryAfterIsHonouredOverComputedBackoff(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{},
	})

	limiter.Observe("flaky", httpx.Outcome{Success: false, RetryAfter: 90 * time.Second})

	state := limiter.State("flaky")
	if want := fake.Now().Add(90 * time.Second); !state.PausedUntil.Equal(want) {
		t.Errorf("PausedUntil = %v, want %v — the provider's own header outranks our backoff",
			state.PausedUntil, want)
	}
}

func TestLimiter_APauseTooLongToWaitForIsReportedNotWaitedOut(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{},
	})
	limiter.Observe("quota", httpx.Outcome{Success: false, RetryAfter: 3 * time.Hour})

	var paused *httpx.PausedError
	if _, err := limiter.Acquire(t.Context(), "quota"); !errors.As(err, &paused) || !errors.Is(err, httpx.ErrCircuitOpen) {
		t.Fatalf("Acquire in a 3 hour pause = %v, want a PausedError", err)
	}
	if want := fake.Now().Add(3 * time.Hour); !paused.Until.Equal(want) {
		t.Errorf("Until = %v, want %v", paused.Until, want)
	}
}

func TestLimiter_BackoffGrowsWithConsecutiveFailures(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{},
	})

	limiter.Observe("flaky", httpx.Outcome{Success: false})
	first := limiter.State("flaky").PausedUntil.Sub(fake.Now())

	limiter.Observe("flaky", httpx.Outcome{Success: false})
	second := limiter.State("flaky").PausedUntil.Sub(fake.Now())

	if second <= first {
		t.Errorf("second failure paused for %v, not longer than the first's %v", second, first)
	}
	if got := limiter.State("flaky").ConsecutiveFailures; got != 2 {
		t.Errorf("ConsecutiveFailures = %d, want 2", got)
	}
}

func TestLimiter_AcquireHonoursContextCancellation(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{MinInterval: time.Hour},
	})

	release, err := limiter.Acquire(t.Context(), "slow")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	release()

	ctx, cancel := newCancellableContext(t)
	waited := make(chan error, 1)
	go func() {
		_, err := limiter.Acquire(ctx, "slow")
		waited <- err
	}()

	fake.BlockUntilWaiters(1)
	cancel()

	if err := <-waited; !errors.Is(err, context.Canceled) {
		t.Errorf("Acquire = %v, want context.Canceled", err)
	}
}

func TestLimiter_ReleaseIsIdempotent(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		Defaults: httpx.Limits{MaxConcurrent: 1},
	})

	release, err := limiter.Acquire(t.Context(), "indexer")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release() // a double release must not free a slot twice

	firstRelease, err := limiter.Acquire(t.Context(), "indexer")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}

	third := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(t.Context(), "indexer")
		if release != nil {
			release()
		}
		third <- err
	}()

	select {
	case err := <-third:
		t.Fatalf("the cap of 1 was exceeded; a double release freed an extra slot (err %v)", err)
	case <-time.After(20 * time.Millisecond):
	}
	firstRelease()
	if err := <-third; err != nil {
		t.Fatalf("third Acquire after the slot freed: %v", err)
	}
}

func newCancellableContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(t.Context())
}

func TestLimiter_ALoneLowPriorityWaiterIsNotHeldBack(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{Defaults: httpx.Limits{MinInterval: time.Second}})
	release, err := limiter.Acquire(httpx.WithPriority(t.Context(), 4), "musicbrainz")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
}

// Requests that queued for a slot behind slow ones still start MinInterval apart once the slots free up.
func TestLimiter_QueuedRequestsTakeTheirPacingTurnAfterTheirSlot(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{MinInterval: time.Second, MaxConcurrent: 2},
	})
	releaseFirst, err := limiter.Acquire(t.Context(), "indexer-a")
	if err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Second)
	releaseSecond, err := limiter.Acquire(t.Context(), "indexer-a")
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan func(), 2)
	for range 2 {
		go func() {
			release, err := limiter.Acquire(t.Context(), "indexer-a")
			if err != nil {
				t.Error(err)
				return
			}
			started <- release
		}()
	}
	for range 3 {
		time.Sleep(20 * time.Millisecond)
		fake.Advance(time.Second)
	}
	time.Sleep(20 * time.Millisecond)
	releaseFirst()
	releaseSecond()

	release := <-started
	defer release()
	select {
	case other := <-started:
		other()
		t.Fatal("both queued requests started at once; the second skipped its pacing turn")
	case <-time.After(50 * time.Millisecond):
	}
	fake.Advance(time.Second)
	(<-started)()
}

func TestLimiter_ARetryAfterOfAYearPausesForAtMostSixHours(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{Defaults: httpx.Limits{}})

	limiter.Observe("indexer", httpx.Outcome{Success: false, RetryAfter: 365 * 24 * time.Hour})

	if want := fake.Now().Add(6 * time.Hour); !limiter.State("indexer").PausedUntil.Equal(want) {
		t.Errorf("PausedUntil = %v, want %v", limiter.State("indexer").PausedUntil, want)
	}
}
