package httpx

import (
	"container/heap"
	"testing"
)

func heapPush(g *gate, waiter *gateWaiter) { heap.Push(&g.waiting[waiter.lane], waiter) }
func heapPop(g *gate, from lane)           { heap.Pop(&g.waiting[from]) }

func turnsOver(waiting [laneCount]bool, rounds int) [laneCount]int {
	var turns laneTurns
	var served [laneCount]int
	for range rounds {
		next, _ := turns.next(DefaultLaneShares, waiting)
		turns.took(DefaultLaneShares, waiting, next)
		served[next]++
	}
	return served
}

func TestLaneTurns_EveryLaneKeepsItsShareWhenAllWait(t *testing.T) {
	if got, want := turnsOver([laneCount]bool{true, true, true}, 100), [laneCount]int{70, 20, 10}; got != want {
		t.Errorf("served %v, want %v", got, want)
	}
}

func TestLaneTurns_AnIdleLanesShareGoesToTheOthers(t *testing.T) {
	if got, want := turnsOver([laneCount]bool{true, false, true}, 80), [laneCount]int{70, 0, 10}; got != want {
		t.Errorf("served %v, want %v", got, want)
	}
	if got, want := turnsOver([laneCount]bool{false, false, true}, 10), [laneCount]int{0, 0, 10}; got != want {
		t.Errorf("served %v, want %v", got, want)
	}
}

func TestLaneOf(t *testing.T) {
	for priority, want := range map[Priority]lane{PriorityUser: laneUser, PriorityFresh: laneFresh, 3: laneBackground, PriorityBackground: laneBackground, 5: laneBackground} {
		if got := laneOf(priority); got != want {
			t.Errorf("laneOf(%d) = %d, want %d", priority, got, want)
		}
	}
}

// A user queue that never empties must still let the other lanes through the gate.
func TestGate_AHammeringUserLaneStillLeavesTheOthersTheirShare(t *testing.T) {
	g := newGate(0, nil)
	for index, priority := range []Priority{PriorityUser, PriorityUser, PriorityUser, PriorityUser, PriorityUser, PriorityUser, PriorityUser, PriorityUser, PriorityUser, PriorityUser, PriorityFresh, PriorityBackground} {
		g.mu.Lock()
		waiter := &gateWaiter{priority: priority, lane: laneOf(priority), arrived: uint64(index), ready: make(chan struct{})}
		heapPush(g, waiter)
		g.mu.Unlock()
	}
	var order []lane
	for range 12 {
		g.mu.Lock()
		waiting := g.waitingLanes()
		next, _ := g.turns.next(DefaultLaneShares, waiting)
		g.turns.took(DefaultLaneShares, waiting, next)
		heapPop(g, next)
		g.mu.Unlock()
		order = append(order, next)
	}
	fresh, background := -1, -1
	for position, served := range order {
		if served == laneFresh && fresh < 0 {
			fresh = position
		}
		if served == laneBackground && background < 0 {
			background = position
		}
	}
	if fresh < 0 || fresh > 4 || background < 0 || background > 11 {
		t.Errorf("order %v: fresh first at %d, background first at %d, want both before the user queue drains", order, fresh, background)
	}
}

func TestLaneTurns_AChangedShareApplies(t *testing.T) {
	shares := LaneShares{User: 1, Fresh: 1, Background: 2}
	var turns laneTurns
	var served [laneCount]int
	waiting := [laneCount]bool{true, true, true}
	for range 40 {
		next, _ := turns.next(shares, waiting)
		turns.took(shares, waiting, next)
		served[next]++
	}
	if want := [laneCount]int{10, 10, 20}; served != want {
		t.Errorf("served %v, want %v", served, want)
	}
}
