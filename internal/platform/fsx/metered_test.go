package fsx

import (
	"io"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

type tally struct {
	latencies []string
	bytes     int64
}

func (t *tally) Latency(path string, _ time.Duration)        { t.latencies = append(t.latencies, path) }
func (t *tally) Transfer(_ string, n int64, _ time.Duration) { t.bytes += n }

func TestMeteredReportsCallsAndBytes(t *testing.T) {
	clk := clock.NewFakeAtEpoch()
	mem := NewMem(clk)
	if err := mem.MkdirAll("/a", 0o755); err != nil {
		t.Fatal(err)
	}
	seen := &tally{}
	fsys := NewMetered(mem, clk, seen)
	w, err := fsys.Create("/a/f")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Stat("/a/f"); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Open("/a/f")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(f); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if seen.bytes != 10 {
		t.Fatalf("bytes = %d, want 10", seen.bytes)
	}
	if len(seen.latencies) != 2 {
		t.Fatalf("latencies = %v, want stat and open", seen.latencies)
	}
}
