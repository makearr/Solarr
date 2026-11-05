package httpx

import (
	"container/heap"
	"context"
	"sync"
)

// gate is a counting semaphore that hands a freed slot to the lane whose turn it is (laneTurns), the most urgent
// waiter of that lane first and the oldest first among equals.
type gate struct {
	mu      sync.Mutex
	free    int
	waiting [laneCount]gateQueue
	turns   laneTurns
	shares  sharesFunc
	arrived uint64
}

type gateWaiter struct {
	priority Priority
	lane     lane
	arrived  uint64
	index    int
	granted  bool
	ready    chan struct{}
}

func newGate(slots int, shares sharesFunc) *gate { return &gate{free: slots, shares: shares} }

// acquire blocks until a slot is handed over or ctx ends.
func (g *gate) acquire(ctx context.Context, priority Priority) error {
	g.mu.Lock()
	if g.free > 0 {
		g.free--
		g.mu.Unlock()
		return nil
	}
	g.arrived++
	waiter := &gateWaiter{priority: priority, lane: laneOf(priority), arrived: g.arrived, ready: make(chan struct{})}
	heap.Push(&g.waiting[waiter.lane], waiter)
	g.mu.Unlock()

	select {
	case <-waiter.ready:
		return nil
	case <-ctx.Done():
		g.mu.Lock()
		defer g.mu.Unlock()
		if waiter.granted {
			g.releaseLocked()
		} else {
			heap.Remove(&g.waiting[waiter.lane], waiter.index)
		}
		return ctx.Err()
	}
}

func (g *gate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseLocked()
}

func (g *gate) waitingLanes() (waiting [laneCount]bool) {
	for index := range g.waiting {
		waiting[index] = g.waiting[index].Len() > 0
	}
	return waiting
}

func (g *gate) releaseLocked() {
	waiting := g.waitingLanes()
	shares := g.shares.read()
	next, anyWaiting := g.turns.next(shares, waiting)
	if !anyWaiting {
		g.free++
		return
	}
	g.turns.took(shares, waiting, next)
	waiter := heap.Pop(&g.waiting[next]).(*gateWaiter)
	waiter.granted = true
	close(waiter.ready)
}

type gateQueue []*gateWaiter

func (q gateQueue) Len() int { return len(q) }
func (q gateQueue) Less(a, b int) bool {
	if q[a].priority != q[b].priority {
		return q[a].priority < q[b].priority
	}
	return q[a].arrived < q[b].arrived
}
func (q gateQueue) Swap(a, b int) {
	q[a], q[b] = q[b], q[a]
	q[a].index, q[b].index = a, b
}
func (q *gateQueue) Push(item any) {
	waiter := item.(*gateWaiter)
	waiter.index = len(*q)
	*q = append(*q, waiter)
}
func (q *gateQueue) Pop() any {
	old := *q
	last := old[len(old)-1]
	*q = old[:len(old)-1]
	return last
}
