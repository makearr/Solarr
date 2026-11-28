package probe

import (
	"context"
	"testing"
)

func TestAdaptiveLimit_GrowsWhileLatencyHoldsAndHalvesWhenItDegrades(t *testing.T) {
	load := 1.0
	limit := newAdaptiveLimit(4, func() float64 { return load })
	finish := func(files int) {
		for range files {
			if !limit.acquire(context.Background()) {
				t.Fatal("acquire failed")
			}
			limit.release()
		}
	}
	if limit.allowed != 1 {
		t.Fatalf("starts at %d, want 1", limit.allowed)
	}
	finish(1)
	finish(2)
	finish(3)
	if limit.allowed != 4 {
		t.Errorf("limit after steady latency = %d, want the maximum 4", limit.allowed)
	}
	load = 3
	finish(1)
	if limit.allowed != 2 {
		t.Errorf("limit after a slowdown = %d, want 2", limit.allowed)
	}
	finish(1)
	if limit.allowed != 1 {
		t.Errorf("limit after a continued slowdown = %d, want 1", limit.allowed)
	}
	load = 1
	finish(5)
	if limit.allowed != 3 {
		t.Errorf("limit after recovery = %d, want regrown step by step to 3", limit.allowed)
	}
}

func TestAdaptiveLimit_AnEndedContextReleasesAWaiter(t *testing.T) {
	limit := newAdaptiveLimit(1, nil)
	if !limit.acquire(context.Background()) {
		t.Fatal("first acquire failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() { done <- limit.acquire(ctx) }()
	cancel()
	if got := <-done; got {
		t.Error("acquire after cancel = true, want false")
	}
}
