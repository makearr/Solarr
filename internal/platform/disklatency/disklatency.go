// Package disklatency measures how long a disk takes to answer, and only when asked to: a probe on an idle disk would
// keep a spun-down drive turning for nothing.
package disklatency

import (
	"sort"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// smoothing weights a new reading against the running average; 0.3 follows a busy disk within a few probes.
const smoothing = 0.3

// Reading is what is known of one disk group.
type Reading struct {
	Group      string
	Last       time.Duration
	Average    time.Duration
	Samples    int
	MeasuredAt time.Time
	Error      string
}

// Monitor keeps the latest reading per disk group.
type Monitor struct {
	fsys        fsx.FS
	clock       clock.Clock
	minInterval time.Duration

	mu       sync.Mutex
	readings map[string]Reading
}

// New builds a monitor that probes a group at most once per minInterval, however often it is asked.
func New(fsys fsx.FS, c clock.Clock, minInterval time.Duration) *Monitor {
	return &Monitor{fsys: fsys, clock: c, minInterval: minInterval, readings: map[string]Reading{}}
}

// Probe times one directory read of path on behalf of group, unless the group was measured less than minInterval
// ago; it reports whether it touched the disk. The caller decides that work is waiting; this never polls by itself.
func (m *Monitor) Probe(group, path string) (Reading, bool) {
	now := m.clock.Now()
	m.mu.Lock()
	previous, known := m.readings[group]
	if known && now.Sub(previous.MeasuredAt) < m.minInterval {
		m.mu.Unlock()
		return previous, false
	}
	m.mu.Unlock()
	return m.measure(group, path, previous, known, now), true
}

// ProbeNow times one directory read whatever the interval says: for an admin who asked.
func (m *Monitor) ProbeNow(group, path string) Reading {
	m.mu.Lock()
	previous, known := m.readings[group]
	m.mu.Unlock()
	return m.measure(group, path, previous, known, m.clock.Now())
}

func (m *Monitor) measure(group, path string, previous Reading, known bool, now time.Time) Reading {
	started := m.clock.Now()
	_, err := m.fsys.ReadDir(path)
	elapsed := m.clock.Now().Sub(started)

	reading := Reading{Group: group, Last: elapsed, Average: elapsed, Samples: 1, MeasuredAt: now}
	if err != nil {
		reading.Error = err.Error()
		reading.Last, reading.Average = 0, previous.Average
		reading.Samples = previous.Samples
	} else if known && previous.Samples > 0 {
		reading.Average = time.Duration(smoothing*float64(elapsed) + (1-smoothing)*float64(previous.Average))
		reading.Samples = previous.Samples + 1
	}
	m.mu.Lock()
	m.readings[group] = reading
	m.mu.Unlock()
	return reading
}

// Latest is the smoothed latency of a group and whether anything has been measured.
func (m *Monitor) Latest(group string) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reading, ok := m.readings[group]
	return reading.Average, ok && reading.Samples > 0
}

// Snapshot is every reading, ordered by group.
func (m *Monitor) Snapshot() []Reading {
	m.mu.Lock()
	out := make([]Reading, 0, len(m.readings))
	for _, reading := range m.readings {
		out = append(out, reading)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}
