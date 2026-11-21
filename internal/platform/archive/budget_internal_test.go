package archive

import (
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// A release in 300 small volumes expands to far more than its first volume, yet is no bomb.
func TestNewBudget_ARatioIsMeasuredAgainstEveryVolume(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	for volume := 1; volume <= 300; volume++ {
		writer, err := fsys.Create(fmt.Sprintf("/d/Release.part%03d.rar", volume))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(make([]byte, 1024))
		_ = writer.Close()
	}
	b := newBudget(fsys, "/d/Release.part001.rar", 1024, Limits{MaxTotalBytes: 1 << 40, MaxExpansionRatio: 10})
	if err := b.addBytes(300 * 1024 * 5); err != nil {
		t.Errorf("5x the whole set is within a ratio of 10: %v", err)
	}
	if err := b.addBytes(300 * 1024 * 6); err == nil {
		t.Error("11x the whole set must exceed a ratio of 10")
	}
}
