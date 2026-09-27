package scan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Directories are identified side by side, but the cursor only ever moves in walk order.
func TestPipeline_ObservesConcurrentlyAndAdvancesInOrder(t *testing.T) {
	var running, peak atomic.Int32
	var mu sync.Mutex
	var advanced []string
	flow := pipeline{
		observe: func(_ context.Context, batch Batch) error {
			now := running.Add(1)
			for {
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
			// Earlier directories take longer, so they finish after later ones.
			delay := time.Duration(10-len(batch.Directory)) * 5 * time.Millisecond
			time.Sleep(delay)
			running.Add(-1)
			return nil
		},
		advance: func(_ context.Context, directory string) error {
			mu.Lock()
			defer mu.Unlock()
			advanced = append(advanced, directory)
			return nil
		},
	}
	var want []string
	for i := 1; i <= 9; i++ {
		directory := fmt.Sprintf("%0*d", i, 0)
		want = append(want, directory)
		if err := flow.submit(t.Context(), Batch{Directory: directory}); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	if err := flow.wait(t.Context()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !slices.Equal(advanced, want) {
		t.Errorf("advanced %q, want %q", advanced, want)
	}
	if got := peak.Load(); got < 2 || got > observeConcurrency {
		t.Errorf("peak concurrency %d, want 2..%d", got, observeConcurrency)
	}
}

// A failed directory stops the cursor before it: a restart repeats it and what followed.
func TestPipeline_AFailureStopsTheCursorBeforeIt(t *testing.T) {
	var advanced []string
	failure := errors.New("provider down")
	flow := pipeline{
		observe: func(_ context.Context, batch Batch) error {
			if batch.Directory == "c" {
				return failure
			}
			return nil
		},
		advance: func(_ context.Context, directory string) error {
			advanced = append(advanced, directory)
			return nil
		},
	}
	var submitErr error
	for _, directory := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		if submitErr = flow.submit(t.Context(), Batch{Directory: directory}); submitErr != nil {
			break
		}
	}
	err := flow.wait(t.Context())
	if !errors.Is(err, failure) && !errors.Is(submitErr, failure) {
		t.Errorf("errors %v / %v, want the failure", submitErr, err)
	}
	if !slices.Equal(advanced, []string{"a", "b"}) {
		t.Errorf("advanced %q, want only a and b", advanced)
	}
}

// A slow directory holds only its own slot: the ones after it keep being observed while the
// cursor waits for it.
func TestPipeline_ASlowDirectoryDoesNotIdleTheOtherSlots(t *testing.T) {
	later := 2 * observeConcurrency
	var observed atomic.Int32
	release := make(chan struct{})
	var advanced []string
	flow := pipeline{
		observe: func(_ context.Context, batch Batch) error {
			if batch.Directory == "slow" {
				<-release
				return nil
			}
			if int(observed.Add(1)) == later {
				close(release)
			}
			return nil
		},
		advance: func(_ context.Context, directory string) error {
			advanced = append(advanced, directory)
			return nil
		},
	}
	done := make(chan error, 1)
	go func() {
		want := []string{"slow"}
		if err := flow.submit(t.Context(), Batch{Directory: "slow"}); err != nil {
			done <- err
			return
		}
		for i := range later {
			directory := fmt.Sprintf("d%02d", i)
			want = append(want, directory)
			if err := flow.submit(t.Context(), Batch{Directory: directory}); err != nil {
				done <- err
				return
			}
		}
		if err := flow.wait(t.Context()); err != nil {
			done <- err
			return
		}
		if !slices.Equal(advanced, want) {
			done <- fmt.Errorf("advanced %q, want %q", advanced, want)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("stalled behind the slow directory after %d others", observed.Load())
	}
}
