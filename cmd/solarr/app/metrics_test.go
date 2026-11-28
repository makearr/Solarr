package app

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/measure"
)

func TestDiskGroupFiguresAddUpTheFoldersOfOneDisk(t *testing.T) {
	var figures diskGroupFigures
	figures.add(measure.Stat{OpsPerMin: 10, BytesPerSec: 100, Degradation: 1.5, Recent: 10 * time.Millisecond})
	figures.add(measure.Stat{OpsPerMin: 5, BytesPerSec: 50, Degradation: 3, Recent: 30 * time.Millisecond})
	figures.add(measure.Stat{OpsPerMin: 1})
	if figures.calls != 16 || figures.bytesPerSec != 150 || figures.degradation != 3 || figures.meanLatencyMs() != 20 {
		t.Errorf("figures = %+v, latency %v; want 16 calls, 150 B/s, worst degradation 3, mean 20 ms of the timed folders", figures, figures.meanLatencyMs())
	}
}
