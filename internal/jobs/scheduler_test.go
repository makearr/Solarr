package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
)

func TestNewScheduler_RejectsAnInvalidExpression(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	if _, err := jobs.NewScheduler("test", "not a cron expression", fake, nil, func(context.Context) {}); err == nil {
		t.Error("an invalid cron expression was accepted")
	}
}

// The schedule fires on its own clock, not on construction or on the caller
// polling it -- Run blocks until the fake clock is advanced to the next
// scheduled minute (store.BackupSchedule's own test proves the same thing for
// the shape this generalizes).
func TestScheduler_FiresAtTheScheduledTime(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fired := make(chan struct{}, 10)
	schedule, err := jobs.NewScheduler("test", "* * * * *", fake, quietLogger(), func(context.Context) {
		fired <- struct{}{}
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		schedule.Run(ctx)
	}()

	fake.BlockUntilWaiters(1)
	fake.Advance(time.Minute)

	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("fire was not called after advancing to the scheduled minute")
	}

	cancel()
	<-done
}

// Two ticks fire twice, not once -- the loop must re-arm itself after every
// fire rather than running down after the first.
func TestScheduler_FiresOnEveryTick(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fired := make(chan struct{}, 10)
	schedule, err := jobs.NewScheduler("test", "* * * * *", fake, quietLogger(), func(context.Context) {
		fired <- struct{}{}
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		schedule.Run(ctx)
	}()

	for range 2 {
		fake.BlockUntilWaiters(1)
		fake.Advance(time.Minute)
		select {
		case <-fired:
		case <-time.After(5 * time.Second):
			t.Fatal("fire was not called for a tick")
		}
	}

	cancel()
	<-done
}

func TestScheduler_StopsWhenContextEnds(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	schedule, err := jobs.NewScheduler("test", "* * * * *", fake, quietLogger(), func(context.Context) {})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		schedule.Run(ctx)
	}()

	fake.BlockUntilWaiters(1)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}
