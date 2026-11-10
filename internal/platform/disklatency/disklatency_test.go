package disklatency

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestProbe_RateLimitsPerGroupAndSmooths(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll("/a", 0o755); err != nil {
		t.Fatal(err)
	}
	monitor := New(fsys, fake, 5*time.Second)

	if _, touched := monitor.Probe("disk:sda", "/a"); !touched {
		t.Fatal("first probe must touch the disk")
	}
	fake.Advance(time.Second)
	if _, touched := monitor.Probe("disk:sda", "/a"); touched {
		t.Fatal("a second probe inside the interval must be answered from memory")
	}
	if _, touched := monitor.Probe("disk:sdb", "/a"); !touched {
		t.Fatal("another group has its own interval")
	}
	fake.Advance(5 * time.Second)
	reading, touched := monitor.Probe("disk:sda", "/a")
	if !touched || reading.Samples != 2 {
		t.Fatalf("after the interval: touched %v samples %d, want true and 2", touched, reading.Samples)
	}
}

func TestProbe_AFailureKeepsTheAverageAndNamesTheError(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	monitor := New(fsx.NewMem(fake), fake, time.Second)
	reading, _ := monitor.Probe("disk:gone", "/missing")
	if reading.Error == "" || reading.Samples != 0 {
		t.Fatalf("got error %q samples %d, want an error and no sample", reading.Error, reading.Samples)
	}
	if _, ok := monitor.Latest("disk:gone"); ok {
		t.Fatal("a failed probe must not count as a latency")
	}
}
