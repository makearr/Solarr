package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
)

// Readers that come within the sharing window get the one read; a failed read is not kept (PERF-22).
func TestSnapshotMemo_SharesOneReadAndKeepsNoFailure(t *testing.T) {
	var memo SnapshotMemo
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	reads := 0
	read := func(context.Context) (apiserver.QueueSnapshot, error) {
		reads++
		return apiserver.QueueSnapshot{Rows: make([]apiserver.QueueRow, reads)}, nil
	}
	first, _ := memo.get(t.Context(), clock, read)
	now = now.Add(time.Second)
	second, _ := memo.get(t.Context(), clock, read)
	if reads != 1 || len(second.Rows) != len(first.Rows) {
		t.Fatalf("reads = %d, rows %d and %d; want one shared read", reads, len(first.Rows), len(second.Rows))
	}
	now = now.Add(2 * time.Second)
	if third, _ := memo.get(t.Context(), clock, read); reads != 2 || len(third.Rows) != 2 {
		t.Errorf("after the window: reads = %d, rows %d; want a new read", reads, len(third.Rows))
	}
	now = now.Add(5 * time.Second)
	failing := func(context.Context) (apiserver.QueueSnapshot, error) {
		return apiserver.QueueSnapshot{}, errors.New("client down")
	}
	if _, err := memo.get(t.Context(), clock, failing); err == nil {
		t.Fatal("a failed read was answered from the memo")
	}
	if _, err := memo.get(t.Context(), clock, read); err != nil || reads != 3 {
		t.Errorf("after a failure: err = %v, reads = %d; want the read made", err, reads)
	}
}
