package parallel_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/makearr/Solarr/internal/platform/parallel"
)

func TestFlightMakesOneCallForConcurrentCallersOfAKey(t *testing.T) {
	var flight parallel.Flight[int]
	var calls atomic.Int32
	var started, joining sync.WaitGroup
	started.Add(1)
	joining.Add(1)
	var first, second sync.WaitGroup
	var sharedFirst, sharedSecond bool
	first.Go(func() {
		_, sharedFirst, _ = flight.Do(t.Context(), "k", func() (int, error) {
			calls.Add(1)
			started.Done()
			joining.Wait()
			// Let the second caller reach Do and wait on this call.
			for range 1000 {
				runtime.Gosched()
			}
			return 42, nil
		})
	})
	started.Wait()
	second.Go(func() {
		joining.Done()
		_, sharedSecond, _ = flight.Do(t.Context(), "k", func() (int, error) {
			calls.Add(1)
			return 7, nil
		})
	})
	first.Wait()
	second.Wait()
	if calls.Load() != 1 || sharedFirst || !sharedSecond {
		t.Errorf("fn ran %d times, shared first=%v second=%v; want one run shared by the second caller", calls.Load(), sharedFirst, sharedSecond)
	}
	if _, shared, _ := flight.Do(t.Context(), "k", func() (int, error) { return 1, nil }); shared {
		t.Error("a finished call was shared")
	}
}

// A joiner whose context ends stops waiting at once; the call runs on for the caller that is still there.
func TestFlightAJoinerWhoseContextEndsStopsWaitingWhileTheCallRunsOn(t *testing.T) {
	var flight parallel.Flight[int]
	release, started := make(chan struct{}), make(chan struct{})
	type answer struct {
		value int
		err   error
	}
	leader := make(chan answer, 1)
	go func() {
		value, _, err := flight.Do(t.Context(), "k", func() (int, error) {
			close(started)
			<-release
			return 42, nil
		})
		leader <- answer{value, err}
	}()
	<-started
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, shared, err := flight.Do(cancelled, "k", func() (int, error) { return 0, nil }); !shared || !errors.Is(err, context.Canceled) {
		t.Errorf("joiner = shared %v, %v; want it to stop waiting with its own cancellation", shared, err)
	}
	close(release)
	if got := <-leader; got.value != 42 || got.err != nil {
		t.Errorf("leader = %+v, want 42 undisturbed", got)
	}
}

// A call that cancels its first caller still answers the others (RACE-4), and a panic is an error, not a hang or a nil.
func TestFlightACancelledLeaderDoesNotTakeTheJoinersDownAndAPanicIsAnError(t *testing.T) {
	var flight parallel.Flight[int]
	release, started := make(chan struct{}), make(chan struct{})
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderDone := make(chan error, 1)
	go func() {
		_, _, err := flight.Do(leaderCtx, "k", func() (int, error) {
			close(started)
			<-release
			return 7, nil
		})
		leaderDone <- err
	}()
	<-started
	joined := make(chan int, 1)
	go func() {
		value, _, _ := flight.Do(t.Context(), "k", func() (int, error) { return 0, nil })
		joined <- value
	}()
	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader = %v, want its own cancellation", err)
	}
	// Let the joiner reach Do and wait on the call before it finishes.
	for range 1000 {
		runtime.Gosched()
	}
	close(release)
	if got := <-joined; got != 7 {
		t.Errorf("joiner = %d, want 7 from the call that went on", got)
	}
	if _, _, err := flight.Do(t.Context(), "p", func() (int, error) { panic("boom") }); err == nil {
		t.Error("a panicking call answered no error")
	}
}
