package store

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

var testEpoch = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

// The queue orders by virtualStart, which does not change as time passes, so a heap stays valid
// (§4.2).
func TestWriteOp_VirtualStartOrdersTheSameAsAgedPriority(t *testing.T) {
	source := rand.New(rand.NewPCG(1, 2))
	priorities := []Priority{PrioBulk, PrioNormal, PrioInteractive}

	for trial := range 200 {
		ops := make([]*writeOp, 0, 8)
		for range 8 {
			priority := priorities[source.IntN(len(priorities))]
			enqueued := testEpoch.Add(-time.Duration(source.IntN(60_000)) * time.Millisecond)
			ops = append(ops, newWriteOp(t.Context(), priority, enqueued, nil))
		}

		byVirtualStart := slices.Clone(ops)
		slices.SortStableFunc(byVirtualStart, func(a, b *writeOp) int {
			return a.virtualStart.Compare(b.virtualStart)
		})

		now := testEpoch
		byAgedPriority := slices.Clone(ops)
		slices.SortStableFunc(byAgedPriority, func(a, b *writeOp) int {
			difference := b.effectivePriority(now) - a.effectivePriority(now)
			switch {
			case difference < 0:
				return -1
			case difference > 0:
				return 1
			default:
				return 0
			}
		})

		for index := range ops {
			if byVirtualStart[index] != byAgedPriority[index] {
				t.Fatalf("trial %d: orderings disagree at position %d; the heap key is not equivalent to §4.2's formula", trial, index)
			}
		}
	}
}

func TestWriteQueue_PopsHigherPriorityFirst(t *testing.T) {
	queue := newWriteQueue()
	bulk := newWriteOp(t.Context(), PrioBulk, testEpoch, nil)
	interactive := newWriteOp(t.Context(), PrioInteractive, testEpoch, nil)
	normal := newWriteOp(t.Context(), PrioNormal, testEpoch, nil)

	for _, op := range []*writeOp{bulk, interactive, normal} {
		if err := queue.push(op); err != nil {
			t.Fatalf("push: %v", err)
		}
	}

	want := []Priority{PrioInteractive, PrioNormal, PrioBulk}
	for index, wantPriority := range want {
		got := queue.pop()
		if got.priority != wantPriority {
			t.Errorf("pop %d = %s, want %s", index, got.priority, wantPriority)
		}
	}
}

// Bulk work must not starve however busy the interactive path is (§4.2).
func TestWriteQueue_AgingLetsBulkOvertakeFreshInteractiveWork(t *testing.T) {
	queue := newWriteQueue()

	// 90 points of aging at 250ms each is 22.5 seconds, which is what it takes
	// for bulk (10) to pass interactive (100).
	staleBulk := newWriteOp(t.Context(), PrioBulk, testEpoch.Add(-30*time.Second), nil)
	freshInteractive := newWriteOp(t.Context(), PrioInteractive, testEpoch, nil)

	if err := queue.push(freshInteractive); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := queue.push(staleBulk); err != nil {
		t.Fatalf("push: %v", err)
	}

	if got := queue.pop(); got != staleBulk {
		t.Error("a bulk operation waiting 30s did not overtake fresh interactive work; it can starve")
	}
}

func TestWriteQueue_YoungBulkStillWaitsBehindInteractive(t *testing.T) {
	queue := newWriteQueue()
	youngBulk := newWriteOp(t.Context(), PrioBulk, testEpoch.Add(-5*time.Second), nil)
	freshInteractive := newWriteOp(t.Context(), PrioInteractive, testEpoch, nil)

	if err := queue.push(youngBulk); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := queue.push(freshInteractive); err != nil {
		t.Fatalf("push: %v", err)
	}

	if got := queue.pop(); got != freshInteractive {
		t.Error("bulk work only 5s old overtook interactive work; aging is too aggressive")
	}
}

func TestWriteQueue_PopDrainsBeforeReportingClosed(t *testing.T) {
	queue := newWriteQueue()
	if err := queue.push(newWriteOp(t.Context(), PrioNormal, testEpoch, nil)); err != nil {
		t.Fatalf("push: %v", err)
	}
	queue.close()

	// Shutdown drains the queue (§15.2 step 5); closing must not discard work.
	if got := queue.pop(); got == nil {
		t.Fatal("pop returned nil with work still queued; closing discarded a pending write")
	}
	if got := queue.pop(); got != nil {
		t.Error("pop returned an operation after the queue was drained")
	}
}

func TestWriteQueue_PushAfterCloseIsRefused(t *testing.T) {
	queue := newWriteQueue()
	queue.close()

	if err := queue.push(newWriteOp(t.Context(), PrioNormal, testEpoch, nil)); err == nil {
		t.Error("push after close succeeded; the write would never be executed")
	}
}
