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
		Defaults: httpx.Limits{RequestsPerMinute: 60, MinInterval: time.Second},
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
		Defaults: httpx.Limits{RequestsPerMinute: 600, MinInterval: time.Second},
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

// The claim ADR-13 exists to make: three subsystems calling one provider draw from one budget,
// not three.
func TestLimiter_SearchRSSAndMetadataShareOneProviderBucket(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 3},
	})

	for subsystem := range 3 {
		release, err := limiter.Acquire(t.Context(), "my-indexer")
		if err != nil {
			t.Fatalf("request %d: %v", subsystem, err)
		}
		release()
	}

	// The minute's budget is spent, whoever spent it.
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
	case <-blocked:
		t.Fatal("a fourth request went through; the three subsystems did not share a bucket")
	default:
	}

	fake.Advance(20 * time.Second) // one token at 3/min
	if err := <-blocked; err != nil {
		t.Fatalf("fourth Acquire after refill: %v", err)
	}
}

func TestLimiter_SeparateProvidersHaveSeparateBudgets(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 1},
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

func TestLimiter_GlobalConcurrencyCapHoldsAcrossProviders(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		GlobalMaxConcurrent: 2,
		Defaults:            httpx.Limits{RequestsPerMinute: 600},
	})

	firstRelease, err := limiter.Acquire(t.Context(), "indexer-a")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	secondRelease, err := limiter.Acquire(t.Context(), "indexer-b")
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	third := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(t.Context(), "indexer-c")
		if release != nil {
			release()
		}
		third <- err
	}()

	select {
	case err := <-third:
		t.Fatalf("a third concurrent request went through with a cap of 2: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	firstRelease()
	if err := <-third; err != nil {
		t.Fatalf("third Acquire after a slot freed: %v", err)
	}
	secondRelease()
}

func TestLimiter_PerProviderConcurrencyCap(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 600, MaxConcurrent: 1},
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
	case <-second:
		t.Fatal("a second concurrent request to the same provider went through with a cap of 1")
	case <-time.After(20 * time.Millisecond):
	}

	firstRelease()
	if err := <-second; err != nil {
		t.Fatalf("second Acquire after release: %v", err)
	}
}

func TestLimiter_DailyCapIsReportedNotWaitedOut(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 600, DailyCap: 2},
	})

	for request := range 2 {
		release, err := limiter.Acquire(t.Context(), "capped")
		if err != nil {
			t.Fatalf("request %d: %v", request, err)
		}
		release()
	}

	// Waiting would block a worker until tomorrow; the provider is reported
	// unavailable instead.
	if _, err := limiter.Acquire(t.Context(), "capped"); !errors.Is(err, httpx.ErrDailyCapReached) {
		t.Fatalf("Acquire past the cap = %v, want ErrDailyCapReached", err)
	}

	fake.Advance(25 * time.Hour)
	release, err := limiter.Acquire(t.Context(), "capped")
	if err != nil {
		t.Fatalf("Acquire after the daily window rolled: %v", err)
	}
	release()
}

func TestLimiter_FailureHalvesTheRateAndPauses(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 60},
	})

	before := limiter.State("flaky")
	limiter.Observe("flaky", httpx.Outcome{Success: false})
	after := limiter.State("flaky")

	if after.EffectivePerMinute >= before.EffectivePerMinute {
		t.Errorf("effective rate %v did not drop from %v", after.EffectivePerMinute, before.EffectivePerMinute)
	}
	if want := before.EffectivePerMinute / 2; after.EffectivePerMinute != want {
		t.Errorf("effective rate = %v, want half of %v", after.EffectivePerMinute, before.EffectivePerMinute)
	}
	if !after.PausedUntil.After(fake.Now()) {
		t.Error("a failure did not pause the provider")
	}
	if !after.Throttled(fake.Now()) {
		t.Error("Throttled = false; the user would never be told (Invariant 29)")
	}
}

func TestLimiter_RetryAfterIsHonouredOverComputedBackoff(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 60},
	})

	limiter.Observe("flaky", httpx.Outcome{Success: false, RetryAfter: 90 * time.Second})

	state := limiter.State("flaky")
	if want := fake.Now().Add(90 * time.Second); !state.PausedUntil.Equal(want) {
		t.Errorf("PausedUntil = %v, want %v — the provider's own header outranks our backoff",
			state.PausedUntil, want)
	}
}

func TestLimiter_RecoveryIsAdditiveNotASnapBack(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 60},
	})

	configured := limiter.State("flaky").ConfiguredPerMinute
	limiter.Observe("flaky", httpx.Outcome{Success: false})
	halved := limiter.State("flaky").EffectivePerMinute

	limiter.Observe("flaky", httpx.Outcome{Success: true})
	afterOne := limiter.State("flaky").EffectivePerMinute

	if afterOne <= halved {
		t.Errorf("one success did not raise the rate: %v", afterOne)
	}
	if afterOne >= configured {
		t.Errorf("one success returned straight to the configured rate %v; recovery must be gradual", configured)
	}

	for range 20 {
		limiter.Observe("flaky", httpx.Outcome{Success: true})
	}
	if got := limiter.State("flaky").EffectivePerMinute; got != configured {
		t.Errorf("sustained success left the rate at %v, want the configured %v", got, configured)
	}
}

func TestLimiter_BackoffGrowsWithConsecutiveFailures(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{
		Defaults: httpx.Limits{RequestsPerMinute: 60},
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
		Defaults: httpx.Limits{RequestsPerMinute: 1, MinInterval: time.Hour},
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
		GlobalMaxConcurrent: 1,
		Defaults:            httpx.Limits{RequestsPerMinute: 600},
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
	case <-third:
		t.Fatal("the global cap of 1 was exceeded; a double release freed an extra slot")
	case <-time.After(20 * time.Millisecond):
	}
	firstRelease()
	<-third
}

func newCancellableContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(t.Context())
}

func TestLimiter_UnratedProviderReportsZeroRatherThanASentinel(t *testing.T) {
	limiter, fake := newLimiter(httpx.Config{Defaults: httpx.Limits{MinInterval: time.Second}})

	state := limiter.State("unrated")
	if state.ConfiguredPerMinute != 0 || state.EffectivePerMinute != 0 {
		t.Errorf("unrated provider reports %v/%v per minute; the internal sentinel reached the UI",
			state.EffectivePerMinute, state.ConfiguredPerMinute)
	}
	if state.Throttled(fake.Now()) {
		t.Error("Throttled = true for an unrated, unpaused provider")
	}
}

// Nobody more urgent waiting: a background request is served as before.
func TestLimiter_ALoneLowPriorityWaiterIsNotHeldBack(t *testing.T) {
	limiter, _ := newLimiter(httpx.Config{Defaults: httpx.Limits{MinInterval: time.Second}})
	release, err := limiter.Acquire(httpx.WithPriority(t.Context(), 4), "musicbrainz")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
}
