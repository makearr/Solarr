package parallel_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/makearr/Solarr/internal/platform/parallel"
)

func TestDo_AnswersEachCallsErrorAtItsIndex(t *testing.T) {
	failure := errors.New("the third failed")
	results := make([]int, 4)
	errs := parallel.Do(t.Context(), 4, 2, func(_ context.Context, index int) error {
		results[index] = index * 10
		if index == 2 {
			return failure
		}
		return nil
	})
	if want := []int{0, 10, 20, 30}; !reflect.DeepEqual(results, want) {
		t.Errorf("results = %v, want %v", results, want)
	}
	if want := []error{nil, nil, failure, nil}; !reflect.DeepEqual(errs, want) {
		t.Errorf("errs = %v, want %v", errs, want)
	}
}

func TestDo_NeverRunsMoreThanWidthAtOnce(t *testing.T) {
	var running, most atomic.Int64
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(3)
	done := make(chan []error)
	go func() {
		done <- parallel.Do(context.Background(), 9, 3, func(context.Context, int) error {
			now := running.Add(1)
			for {
				seen := most.Load()
				if now <= seen || most.CompareAndSwap(seen, now) {
					break
				}
			}
			started.Done()
			<-release
			running.Add(-1)
			return nil
		})
	}()

	started.Wait() // three are in; a fourth would have made this Done panic on a negative counter
	if got := running.Load(); got != 3 {
		t.Errorf("%d calls running with the first three held, want 3", got)
	}
	started.Add(6)
	close(release)
	<-done
	if got := most.Load(); got != 3 {
		t.Errorf("at most %d calls ran at once, want 3", got)
	}
}

func TestDo_TurnsAPanicIntoThatCallsError(t *testing.T) {
	errs := parallel.Do(t.Context(), 3, 0, func(_ context.Context, index int) error {
		if index == 1 {
			panic("a nil map was written")
		}
		return nil
	})
	if errs[0] != nil || errs[2] != nil {
		t.Errorf("the other calls failed: %v", errs)
	}
	var recovered *parallel.PanicError
	if !errors.As(errs[1], &recovered) {
		t.Fatalf("errs[1] = %v, want a PanicError", errs[1])
	}
	if recovered.Value != "a nil map was written" || len(recovered.Stack) == 0 {
		t.Errorf("PanicError = %q with %d bytes of stack, want the panic value and a stack", recovered.Value, len(recovered.Stack))
	}
}

func TestDo_WithNothingToDoAnswersNothing(t *testing.T) {
	errs := parallel.Do(t.Context(), 0, 4, func(context.Context, int) error {
		t.Error("fn was called")
		return nil
	})
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none", errs)
	}
}
