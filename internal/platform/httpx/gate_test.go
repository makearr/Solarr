package httpx

import (
	"context"
	"testing"
	"time"
)

func waitForWaiters(t *testing.T, g *gate, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		got := 0
		for index := range g.waiting {
			got += g.waiting[index].Len()
		}
		g.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("gate never reached %d waiters", want)
}

func TestGate_AFreedSlotGoesToTheMostUrgentWaiterOldestFirst(t *testing.T) {
	g := newGate(1, nil)
	if err := g.acquire(context.Background(), PriorityUser); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	order := make(chan string, 3)
	for index, entry := range []struct {
		name     string
		priority Priority
	}{{"background", PriorityBackground}, {"fresh", PriorityFresh}, {"user", PriorityUser}} {
		go func() {
			if err := g.acquire(context.Background(), entry.priority); err == nil {
				order <- entry.name
				g.release()
			}
		}()
		waitForWaiters(t, g, index+1)
	}
	g.release()
	var got []string
	for range 3 {
		got = append(got, <-order)
	}
	if got[0] != "user" || got[1] != "fresh" || got[2] != "background" {
		t.Errorf("served %v, want [user fresh background]", got)
	}
}

func TestGate_ACancelledWaiterGivesUpItsPlace(t *testing.T) {
	g := newGate(1, nil)
	_ = g.acquire(context.Background(), PriorityUser)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.acquire(ctx, PriorityUser) }()
	waitForWaiters(t, g, 1)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled acquire succeeded")
	}
	g.release()
	if err := g.acquire(context.Background(), PriorityBackground); err != nil {
		t.Errorf("slot was lost to the cancelled waiter: %v", err)
	}
}
