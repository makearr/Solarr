package app

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/makearr/Solarr/internal/platform/measure"
)

// timings feeds the recorder from the filesystem seam and the HTTP clients: disk calls are filed under the root
// folder holding the path, requests under the provider that was asked.
type timings struct {
	rec *measure.Recorder
	// roots lists the root folder paths; set once the settings cache exists, nil before.
	roots atomic.Pointer[func() []string]
}

func (t *timings) rootOf(path string) string {
	list := t.roots.Load()
	if list == nil {
		return ""
	}
	best := ""
	for _, root := range (*list)() {
		if len(root) > len(best) && (path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")) {
			best = root
		}
	}
	return best
}

// Latency implements fsx.Meter.
func (t *timings) Latency(path string, d time.Duration) {
	t.rec.Latency(measure.Disk, t.rootOf(path), d)
}

// Transfer implements fsx.Meter.
func (t *timings) Transfer(path string, bytes int64, d time.Duration) {
	t.rec.Transfer(measure.Disk, t.rootOf(path), bytes, d)
}

// Answered implements httpx.Meter.
func (t *timings) Answered(provider string, d time.Duration) { t.rec.Latency(measure.Net, provider, d) }

// Received implements httpx.Meter.
func (t *timings) Received(provider string, bytes int64, d time.Duration) {
	t.rec.Transfer(measure.Net, provider, bytes, d)
}
