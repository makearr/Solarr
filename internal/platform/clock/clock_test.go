package clock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

var (
	_ clock.Clock = clock.System{}
	_ clock.Clock = (*clock.Fake)(nil)
)

func TestFake_NowAdvancesOnlyWhenTold(t *testing.T) {
	start := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)

	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	f.Advance(90 * time.Second)
	if got, want := f.Now(), start.Add(90*time.Second); !got.Equal(want) {
		t.Errorf("after Advance, Now() = %v, want %v", got, want)
	}
	if got, want := f.Since(start), 90*time.Second; got != want {
		t.Errorf("Since(start) = %v, want %v", got, want)
	}
	if got, want := f.Until(start.Add(2*time.Minute)), 30*time.Second; got != want {
		t.Errorf("Until = %v, want %v", got, want)
	}
}

func TestFake_TimerFiresAtItsDeadlineNotBefore(t *testing.T) {
	f := clock.NewFakeAtEpoch()
	timer := f.NewTimer(time.Minute)

	f.Advance(59 * time.Second)
	select {
	case at := <-timer.C():
		t.Fatalf("timer fired early at %v", at)
	default:
	}

	f.Advance(time.Second)
	select {
	case at := <-timer.C():
		if want := f.Now(); !at.Equal(want) {
			t.Errorf("timer delivered %v, want the deadline %v", at, want)
		}
	default:
		t.Fatal("timer did not fire at its deadline")
	}
}

func TestFake_TimerStopAndReset(t *testing.T) {
	f := clock.NewFakeAtEpoch()
	timer := f.NewTimer(time.Minute)

	if !timer.Stop() {
		t.Error("Stop() = false on a pending timer, want true")
	}
	if timer.Stop() {
		t.Error("second Stop() = true, want false")
	}
	f.Advance(time.Hour)
	select {
	case <-timer.C():
		t.Fatal("stopped timer fired")
	default:
	}

	if timer.Reset(time.Minute) {
		t.Error("Reset() = true on a stopped timer, want false")
	}
	f.Advance(time.Minute)
	select {
	case <-timer.C():
	default:
		t.Fatal("reset timer did not fire")
	}
}

func TestFake_TickerFiresRepeatedlyAndDropsUnreadTicks(t *testing.T) {
	f := clock.NewFakeAtEpoch()
	ticker := f.NewTicker(time.Second)
	defer ticker.Stop()

	// Three intervals pass with nothing reading: time.Ticker keeps only the latest.
	f.Advance(3 * time.Second)
	select {
	case at := <-ticker.C():
		if want := f.Now().Add(-2 * time.Second); !at.Equal(want) {
			t.Errorf("first tick = %v, want the earliest deadline %v", at, want)
		}
	default:
		t.Fatal("ticker did not fire")
	}
	select {
	case at := <-ticker.C():
		t.Fatalf("a second buffered tick was delivered at %v; ticks must coalesce", at)
	default:
	}

	f.Advance(time.Second)
	select {
	case <-ticker.C():
	default:
		t.Fatal("ticker did not fire again after Advance")
	}
}

func TestFake_TickerStopEndsIt(t *testing.T) {
	f := clock.NewFakeAtEpoch()
	ticker := f.NewTicker(time.Second)
	ticker.Stop()

	f.Advance(time.Hour)
	select {
	case <-ticker.C():
		t.Fatal("stopped ticker fired")
	default:
	}
	if got := f.Waiters(); got != 0 {
		t.Errorf("Waiters() = %d after Stop, want 0", got)
	}
}

func TestFake_SleepReturnsWhenTimePasses(t *testing.T) {
	f := clock.NewFakeAtEpoch()
	done := make(chan error, 1)
	go func() { done <- f.Sleep(t.Context(), time.Minute) }()

	f.BlockUntilWaiters(1)
	f.Advance(time.Minute)

	if err := <-done; err != nil {
		t.Errorf("Sleep() = %v, want nil", err)
	}
	if got := f.Waiters(); got != 0 {
		t.Errorf("Waiters() = %d after Sleep returned, want 0", got)
	}
}

func TestFake_SleepReturnsContextErrorOnCancel(t *testing.T) {
	f := clock.NewFakeAtEpoch()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.Sleep(ctx, time.Hour) }()

	f.BlockUntilWaiters(1)
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep() = %v, want context.Canceled", err)
	}
	if got := f.Waiters(); got != 0 {
		t.Errorf("Waiters() = %d after cancellation, want 0; the waiter leaked", got)
	}
}

func TestFake_WaitersFireInDeadlineOrder(t *testing.T) {
	f := clock.NewFakeAtEpoch()
	late := f.NewTimer(2 * time.Minute)
	early := f.NewTimer(time.Minute)

	f.Advance(3 * time.Minute)

	earlyAt, lateAt := <-early.C(), <-late.C()
	if !earlyAt.Before(lateAt) {
		t.Errorf("fired out of order: early %v, late %v", earlyAt, lateAt)
	}
}

func TestFake_SetMovesBackwardsWithoutFiring(t *testing.T) {
	start := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	timer := f.NewTimer(time.Minute)

	f.Set(start.Add(-time.Hour))
	if got, want := f.Now(), start.Add(-time.Hour); !got.Equal(want) {
		t.Errorf("Now() = %v, want %v", got, want)
	}
	select {
	case <-timer.C():
		t.Fatal("moving time backwards fired a timer")
	default:
	}
}

func TestSystem_SleepObservesCancellation(t *testing.T) {
	c := clock.NewSystem()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep() = %v, want context.Canceled", err)
	}
}

func TestSystem_NowMovesForward(t *testing.T) {
	c := clock.NewSystem()
	before := c.Now()
	if err := c.Sleep(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("Sleep() = %v", err)
	}
	if !c.Now().After(before) {
		t.Error("system clock did not advance across a sleep")
	}
}
