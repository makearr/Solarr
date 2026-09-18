package logbuffer_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/platform/logbuffer"
)

func newRing(t *testing.T, capacity int) (*logbuffer.Ring, *slog.Logger) {
	t.Helper()
	ring := logbuffer.NewRing(slog.NewTextHandler(io.Discard, nil), capacity)
	return ring, slog.New(ring)
}

func TestRing_KeepsRecordsNewestFirst(t *testing.T) {
	ring, logger := newRing(t, 10)
	logger.Info("first")
	logger.Info("second")

	records := ring.Records(logbuffer.Filter{})
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].Message != "second" || records[1].Message != "first" {
		t.Errorf("order = %q then %q, want newest first", records[0].Message, records[1].Message)
	}
}

// The oldest records fall off rather than the buffer growing: bounded is the
// whole point of keeping logs in memory.
func TestRing_DropsTheOldestOnceFull(t *testing.T) {
	ring, logger := newRing(t, 3)
	for _, message := range []string{"a", "b", "c", "d", "e"} {
		logger.Info(message)
	}

	records := ring.Records(logbuffer.Filter{})
	if len(records) != 3 {
		t.Fatalf("records = %d, want the capacity 3", len(records))
	}
	if records[0].Message != "e" || records[2].Message != "c" {
		t.Errorf("kept %q..%q, want e..c", records[0].Message, records[2].Message)
	}
}

// A logger derived with .With() must land in the same buffer. The first
// version of this handler copied the slice header instead of sharing it, so
// every record from a derived logger went somewhere nobody could read.
func TestRing_CapturesFromADerivedLogger(t *testing.T) {
	ring, logger := newRing(t, 10)
	derived := logger.With("component", "scan")
	derived.Info("from the derived logger")

	records := ring.Records(logbuffer.Filter{})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1 from the derived logger", len(records))
	}
	if records[0].Message != "from the derived logger" {
		t.Errorf("message = %q", records[0].Message)
	}
}

func TestRing_FiltersByLevel(t *testing.T) {
	ring, logger := newRing(t, 10)
	logger.Info("quiet")
	logger.Error("loud")

	records := ring.Records(logbuffer.Filter{MinLevel: slog.LevelWarn})
	if len(records) != 1 || records[0].Message != "loud" {
		t.Errorf("records = %+v, want only the error", records)
	}
}

// The search covers attributes, not just the message: "which run touched
// work 42" is the question an operator actually has.
func TestRing_SearchesMessageAndAttributes(t *testing.T) {
	ring, logger := newRing(t, 10)
	logger.Info("scan finished", "work_id", 42)
	logger.Info("something else")

	byAttr := ring.Records(logbuffer.Filter{Contains: "42"})
	if len(byAttr) != 1 || byAttr[0].Message != "scan finished" {
		t.Errorf("attribute search = %+v, want the scan record", byAttr)
	}
	byMessage := ring.Records(logbuffer.Filter{Contains: "SOMETHING"})
	if len(byMessage) != 1 {
		t.Errorf("message search = %+v, want one case-insensitive match", byMessage)
	}
}

func TestRing_ConcurrentWritesAndReads(t *testing.T) {
	ring, logger := newRing(t, 64)
	var waiting sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		waiting.Add(2)
		go func() {
			defer waiting.Done()
			for i := 0; i < 50; i++ {
				logger.Info("write")
			}
		}()
		go func() {
			defer waiting.Done()
			for i := 0; i < 50; i++ {
				ring.Records(logbuffer.Filter{Limit: 5})
			}
		}()
	}
	waiting.Wait()
	if got := len(ring.Records(logbuffer.Filter{})); got != 64 {
		t.Errorf("records = %d, want the capacity 64", got)
	}
}
