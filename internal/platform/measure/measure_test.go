package measure

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

func feed(r *Recorder, fake *clock.Fake, ops int, d time.Duration) {
	for i := 0; i < ops; i++ {
		r.Latency(Disk, "/lib", d)
	}
	fake.Advance(time.Minute)
}

func TestDegradationAgainstBaseline(t *testing.T) {
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	r := New(fake)
	for i := 0; i < 5; i++ {
		feed(r, fake, 20, 2*time.Millisecond)
	}
	r.Latency(Disk, "/lib", 2*time.Millisecond) // opens the new minute so the last one folds into the baseline
	if got := r.Load(Disk); got != 1 {
		t.Fatalf("steady disk load = %v, want 1", got)
	}
	for i := 0; i < 20; i++ {
		r.Latency(Disk, "/lib", 10*time.Millisecond)
	}
	stats := r.Snapshot()
	if len(stats) != 1 || stats[0].Baseline != 2*time.Millisecond {
		t.Fatalf("stats = %+v", stats)
	}
	if got := r.Load(Disk); got < 2.5 || got > 5 {
		t.Fatalf("degraded load = %v, want 2.5 to 5", got)
	}
	if got := r.Load(Net); got != 1 {
		t.Fatalf("net load = %v, want 1", got)
	}
}

func TestTooFewReadingsSayNothing(t *testing.T) {
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	r := New(fake)
	for i := 0; i < 3; i++ {
		feed(r, fake, 20, time.Millisecond)
	}
	r.Latency(Disk, "/lib", time.Second)
	if got := r.Load(Disk); got != 1 {
		t.Fatalf("load = %v, want 1", got)
	}
}

func TestTransferShowsAsBytesPerSecond(t *testing.T) {
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	r := New(fake)
	r.Transfer(Disk, "/lib", 6000, time.Second)
	fake.Advance(time.Minute)
	r.Transfer(Disk, "/lib", 1, time.Second)
	if got := r.Snapshot()[0].BytesPerSec; got != 100 {
		t.Fatalf("bytes/s = %v, want 100", got)
	}
}

func TestLoadOf_OneSeriesOnlyAndOneForAnUnknownKey(t *testing.T) {
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	r := New(fake)
	for range 5 {
		for range 20 {
			r.Latency(Disk, "/a", 2*time.Millisecond)
			r.Latency(Disk, "/b", 2*time.Millisecond)
		}
		fake.Advance(time.Minute)
	}
	for range 20 {
		r.Latency(Disk, "/a", 10*time.Millisecond)
		r.Latency(Disk, "/b", 2*time.Millisecond)
	}
	if got := r.LoadOf(Disk, "/a"); got < 2.5 {
		t.Errorf("LoadOf(/a) = %v, want a slowdown", got)
	}
	if got := r.LoadOf(Disk, "/b"); got != 1 {
		t.Errorf("LoadOf(/b) = %v, want 1", got)
	}
	if got := r.LoadOf(Disk, "/none"); got != 1 {
		t.Errorf("LoadOf(/none) = %v, want 1", got)
	}
}
