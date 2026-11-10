// Package iogate shares one disk group's IO between the tasks that want it: each task takes a slot for as long as it
// reads or writes, the most urgent waiter gets the next free one, and a waiter that has waited long enough moves up
// so a steady stream of urgent work cannot starve it.
package iogate

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Class is how urgent a kind of disk work is; a lower number is more urgent.
type Class int

// The classes, most urgent first (user's order, 2026-11-10).
const (
	ClassPlayback Class = iota
	ClassImport
	ClassRename
	ClassTagWriteback
	ClassDrain
	ClassScan
	ClassProbe
	ClassStatistics
	ClassRecycleBin
	ClassPreviews
	classCount
)

// firstBulk is the first class held one slot short of the group's capacity, so the urgent classes always find room.
const firstBulk = ClassScan

// Options tune a Gate.
type Options struct {
	// Capacity is how many tasks may use a group at once; below 1 is read as 1.
	Capacity func(group string) int
	// AgeStep is how long a waiter waits for each class it moves up.
	AgeStep time.Duration
	// OnWait is told when a task starts waiting on a group, so the caller can measure that disk's latency then and only then.
	OnWait func(group string)
}

// Gate hands out slots per disk group.
type Gate struct {
	clock   clock.Clock
	options Options

	mu     sync.Mutex
	groups map[string]*groupState
}

type groupState struct {
	running   map[*holder]struct{}
	waiting   []*waiter
	lastTouch time.Time
}

type holder struct{ class Class }

type waiter struct {
	class   Class
	since   time.Time
	granted chan *holder
}

// New builds a gate. A zero AgeStep is 30 seconds.
func New(c clock.Clock, options Options) *Gate {
	if options.AgeStep <= 0 {
		options.AgeStep = 30 * time.Second
	}
	return &Gate{clock: c, options: options, groups: map[string]*groupState{}}
}

// Acquire waits for a slot on group and returns the function that gives it back. It fails only when ctx ends first.
func (g *Gate) Acquire(ctx context.Context, group string, class Class) (release func(), err error) {
	now := g.clock.Now()
	g.mu.Lock()
	state := g.state(group)
	state.lastTouch = now
	entry := &waiter{class: class, since: now, granted: make(chan *holder, 1)}
	state.waiting = append(state.waiting, entry)
	g.admitLocked(group, state)
	waiting := len(state.waiting) > 0 && containsWaiter(state.waiting, entry)
	g.mu.Unlock()

	if waiting && g.options.OnWait != nil {
		g.options.OnWait(group)
	}
	select {
	case held := <-entry.granted:
		return func() { g.release(group, held) }, nil
	case <-ctx.Done():
		g.mu.Lock()
		state.waiting = removeWaiter(state.waiting, entry)
		g.mu.Unlock()
		// A grant may have landed between the cancellation and the lock: hand that slot straight back.
		select {
		case held := <-entry.granted:
			g.release(group, held)
		default:
		}
		return nil, ctx.Err()
	}
}

// Idle reports whether nothing has used or waited for the group within the last window: the caller's cue that a
// background check of that disk can be skipped.
func (g *Gate) Idle(group string, window time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	state, known := g.groups[group]
	if !known {
		return true
	}
	return len(state.running) == 0 && len(state.waiting) == 0 && g.clock.Now().Sub(state.lastTouch) >= window
}

// Waiting is how many tasks wait on the group.
func (g *Gate) Waiting(group string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if state, known := g.groups[group]; known {
		return len(state.waiting)
	}
	return 0
}

func (g *Gate) state(group string) *groupState {
	state, known := g.groups[group]
	if !known {
		state = &groupState{running: map[*holder]struct{}{}}
		g.groups[group] = state
	}
	return state
}

func (g *Gate) release(group string, held *holder) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.state(group)
	delete(state.running, held)
	state.lastTouch = g.clock.Now()
	g.admitLocked(group, state)
}

// admitLocked starts waiters, most urgent first, for as long as there is room for them.
func (g *Gate) admitLocked(group string, state *groupState) {
	capacity := 1
	if g.options.Capacity != nil {
		capacity = max(1, g.options.Capacity(group))
	}
	now := g.clock.Now()
	for len(state.waiting) > 0 && len(state.running) < capacity {
		sort.SliceStable(state.waiting, func(i, j int) bool {
			a, b := g.effective(state.waiting[i], now), g.effective(state.waiting[j], now)
			if a != b {
				return a < b
			}
			return state.waiting[i].since.Before(state.waiting[j].since)
		})
		admitted := false
		for i, candidate := range state.waiting {
			if candidate.class >= firstBulk && capacity > 1 && bulkRunning(state) >= capacity-1 {
				continue
			}
			held := &holder{class: candidate.class}
			state.running[held] = struct{}{}
			candidate.granted <- held
			state.waiting = append(state.waiting[:i], state.waiting[i+1:]...)
			admitted = true
			break
		}
		if !admitted {
			return
		}
	}
}

// effective is the class a waiter is treated as having: one better for each AgeStep it has waited.
func (g *Gate) effective(entry *waiter, now time.Time) int {
	return max(0, int(entry.class)-int(now.Sub(entry.since)/g.options.AgeStep))
}

func bulkRunning(state *groupState) int {
	count := 0
	for held := range state.running {
		if held.class >= firstBulk {
			count++
		}
	}
	return count
}

func containsWaiter(list []*waiter, entry *waiter) bool {
	for _, candidate := range list {
		if candidate == entry {
			return true
		}
	}
	return false
}

func removeWaiter(list []*waiter, entry *waiter) []*waiter {
	for i, candidate := range list {
		if candidate == entry {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}
