package batch_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/importing/batch"
)

func TestResolveAll_AnswersEveryIndexWithFewAtOnce(t *testing.T) {
	const units = 12
	var running, widest atomic.Int32
	answers := make([]int, units)
	batch.ResolveAll(context.Background(), units, func(_ context.Context, index int) {
		now := running.Add(1)
		for seen := widest.Load(); now > seen && !widest.CompareAndSwap(seen, now); seen = widest.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		answers[index] = index + 1
	})
	for index, answer := range answers {
		if answer != index+1 {
			t.Fatalf("answers[%d] = %d, want %d", index, answer, index+1)
		}
	}
	if got := widest.Load(); got < 2 || got > 4 {
		t.Fatalf("widest = %d, want between 2 and 4", got)
	}
}

func TestResolveAll_APanicLeavesOnlyItsSlotEmpty(t *testing.T) {
	answers := make([]int, 3)
	batch.ResolveAll(context.Background(), 3, func(_ context.Context, index int) {
		if index == 1 {
			panic("provider exploded")
		}
		answers[index] = 1
	})
	if want := []int{1, 0, 1}; answers[0] != want[0] || answers[1] != want[1] || answers[2] != want[2] {
		t.Fatalf("answers = %v, want %v", answers, want)
	}
}
