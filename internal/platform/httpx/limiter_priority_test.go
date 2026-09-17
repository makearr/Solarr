package httpx

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// Tested at the take decision itself: through goroutines, the scheduler's wake order decides who
// runs first, so a test there passed with the lanes switched off.
func TestTryTake_ADueTokenIsHeldForAMoreUrgentWaiter(t *testing.T) {
	limiter := NewLimiter(clock.NewFakeAtEpoch(), Config{Defaults: Limits{MinInterval: time.Second}}, NoJitter)
	limiter.wait("musicbrainz", 1, 1)

	if wait, err := limiter.tryTake("musicbrainz", 4); err != nil || wait != laneRecheck {
		t.Fatalf("background tryTake = %v, %v; want it passed over for %v", wait, err, laneRecheck)
	}
	if wait, err := limiter.tryTake("musicbrainz", 1); err != nil || wait != 0 {
		t.Errorf("interactive tryTake = %v, %v; want the token, which the passed-over waiter left unspent", wait, err)
	}
}

// A waiter that gave up must stop holding everyone else back.
func TestAcquire_AGivenUpWaiterNoLongerHoldsOthersBack(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	limiter := NewLimiter(fake, Config{Defaults: Limits{MinInterval: time.Second}}, NoJitter)
	release, err := limiter.Acquire(context.Background(), "musicbrainz")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := limiter.Acquire(WithPriority(ctx, 1), "musicbrainz")
		done <- err
	}()
	fake.BlockUntilWaiters(1)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("the cancelled Acquire succeeded")
	}

	fake.Advance(time.Second)
	if wait, err := limiter.tryTake("musicbrainz", 4); err != nil || wait != 0 {
		t.Errorf("background tryTake = %v, %v; want the token once the urgent waiter left", wait, err)
	}
}
