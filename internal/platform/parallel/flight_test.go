package parallel_test

import (
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
		_, sharedFirst, _ = flight.Do("k", func() (int, error) {
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
		_, sharedSecond, _ = flight.Do("k", func() (int, error) {
			calls.Add(1)
			return 7, nil
		})
	})
	first.Wait()
	second.Wait()
	if calls.Load() != 1 || sharedFirst || !sharedSecond {
		t.Errorf("fn ran %d times, shared first=%v second=%v; want one run shared by the second caller", calls.Load(), sharedFirst, sharedSecond)
	}
	if _, shared, _ := flight.Do("k", func() (int, error) { return 1, nil }); shared {
		t.Error("a finished call was shared")
	}
}
