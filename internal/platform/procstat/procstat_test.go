package procstat

import (
	"strconv"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func write(t *testing.T, fsys *fsx.Mem, path, text string) {
	t.Helper()
	w, err := fsys.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRatesBetweenTwoReadings(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll("/proc/self", 0o755); err != nil {
		t.Fatal(err)
	}
	set := func(utime, stime, read, written int) {
		write(t, fsys, "/proc/self/stat", "42 (sol arr) S 1 1 1 0 -1 0 0 0 0 0 "+strconv.Itoa(utime)+" "+strconv.Itoa(stime)+" 0 0 20 0 1 0 0 0 0\n")
		write(t, fsys, "/proc/self/statm", "100 25 10 1 0 5 0\n")
		write(t, fsys, "/proc/self/io", "rchar: "+strconv.Itoa(read)+"\nwchar: "+strconv.Itoa(written)+"\nread_bytes: 0\nwrite_bytes: 0\n")
	}
	sampler := New(fsys, fake)
	set(100, 50, 1000, 0)
	if _, ok := sampler.Rates(); ok {
		t.Fatal("first reading has nothing to compare with")
	}
	fake.Advance(time.Minute)
	set(400, 200, 7000, 600)
	rates, ok := sampler.Rates()
	if !ok {
		t.Fatal("no rates")
	}
	if rates.CPUPercent != 7.5 || rates.ResidentBytes != 25*4096 || rates.ReadBytesPerSec != 100 || rates.WriteBytesPerSec != 10 {
		t.Fatalf("rates = %+v", rates)
	}
}
