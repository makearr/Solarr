package store

import (
	"container/heap"
	"context"
	"database/sql"
	"sync"
	"time"
)

// Priority orders work in the write queue. An API-driven user action must not
// wait behind a library scan (ADR-3).
type Priority int

const (
	// PrioBulk is scans, imports and backfill.
	PrioBulk Priority = 10
	// PrioNormal is planner output, a single import, metadata writes.
	PrioNormal Priority = 50
	// PrioInteractive is anything a user is waiting on.
	PrioInteractive Priority = 100
)

// String names the priority for logs and metrics.
func (p Priority) String() string {
	switch {
	case p >= PrioInteractive:
		return "interactive"
	case p >= PrioNormal:
		return "normal"
	default:
		return "bulk"
	}
}

// agingInterval is how much waiting is worth one point of priority (§4.2).
const agingInterval = 250 * time.Millisecond

type writeOp struct {
	priority Priority
	enqueued time.Time

	// virtualStart is enqueued shifted back by the priority's worth of aging.
	virtualStart time.Time

	ctx context.Context

	// execute receives the write connection, not a transaction, so the queue can
	// also serialise what SQLite refuses to run inside one, such as VACUUM INTO.
	execute func(context.Context, *sql.DB) error

	// exemptFromBudget marks the one write that is legitimately unbounded: a migration creating an
	// index over an existing library (Invariant 2).
	exemptFromBudget bool

	result chan error
	index  int
}

func newWriteOp(ctx context.Context, priority Priority, now time.Time, execute func(context.Context, *sql.DB) error) *writeOp {
	return &writeOp{
		priority:     priority,
		enqueued:     now,
		virtualStart: now.Add(-time.Duration(priority) * agingInterval),
		ctx:          ctx,
		execute:      execute,
		result:       make(chan error, 1),
	}
}

// effectivePriority is what virtualStart encodes, kept for logs and for the test
// that proves the two orderings agree.
func (o *writeOp) effectivePriority(now time.Time) float64 {
	return float64(o.priority) + float64(now.Sub(o.enqueued))/float64(agingInterval)
}

type opHeap []*writeOp

func (h opHeap) Len() int { return len(h) }

// Less puts the earliest virtual start first, which is the highest aged priority.
func (h opHeap) Less(i, j int) bool { return h[i].virtualStart.Before(h[j].virtualStart) }

func (h opHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}

func (h *opHeap) Push(value any) {
	op := value.(*writeOp)
	op.index = len(*h)
	*h = append(*h, op)
}

func (h *opHeap) Pop() any {
	old := *h
	last := len(old) - 1
	op := old[last]
	old[last] = nil
	*h = old[:last]
	return op
}

// writeQueue feeds the single writer goroutine.
type writeQueue struct {
	mu     sync.Mutex
	ready  sync.Cond
	ops    opHeap
	closed bool
}

func newWriteQueue() *writeQueue {
	q := &writeQueue{}
	q.ready.L = &q.mu
	return q
}

func (q *writeQueue) push(op *writeOp) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	heap.Push(&q.ops, op)
	q.ready.Signal()
	return nil
}

// pop blocks until work arrives, returning nil only once the queue is closed
// and drained, which is what bounds the shutdown drain (section 15.2).
func (q *writeQueue) pop() *writeOp {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.ops) == 0 && !q.closed {
		q.ready.Wait()
	}
	if len(q.ops) == 0 {
		return nil
	}
	return heap.Pop(&q.ops).(*writeOp)
}

func (q *writeQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.ready.Broadcast()
}

func (q *writeQueue) depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ops)
}
