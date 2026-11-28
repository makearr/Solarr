// Package measure keeps Solarr's own timings: how long its disk operations and outbound requests take, per root folder
// and per host. It never probes; it only records what the work already did, so it adds no traffic and wakes no disk.
package measure

import (
	"sort"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Kind separates disk series from network series.
type Kind string

// The two kinds of series.
const (
	Disk Kind = "disk"
	Net  Kind = "net"
)

// Window is how many minutes of history a series keeps.
const Window = 60

const (
	// minOps is how many latency readings a minute needs before it says anything about the baseline.
	minOps = 8
	// riseWeight lets the baseline follow a slower normal gently; a sustained slowdown still reads as degradation for hours.
	riseWeight = 0.02
	// fallWeight lets the baseline find a faster normal within a few minutes.
	fallWeight = 0.5
)

type bucket struct {
	minute       int64
	ops          int64
	latencyTotal time.Duration
	worst        time.Duration
	bytes        int64
	busy         time.Duration
}

type series struct {
	buckets  [Window]bucket
	baseline time.Duration
}

type seriesKey struct {
	kind Kind
	key  string
}

// Recorder collects timings; safe for concurrent use.
type Recorder struct {
	clock  clock.Clock
	mu     sync.Mutex
	series map[seriesKey]*series
}

// New builds an empty recorder.
func New(c clock.Clock) *Recorder {
	return &Recorder{clock: c, series: map[seriesKey]*series{}}
}

func (r *Recorder) current(kind Kind, key string) (*series, *bucket) {
	k := seriesKey{kind, key}
	s := r.series[k]
	if s == nil {
		s = &series{}
		r.series[k] = s
	}
	minute := r.clock.Now().Unix() / 60
	b := &s.buckets[minute%Window]
	if b.minute != minute {
		s.fold(minute)
		*b = bucket{minute: minute}
	}
	return s, b
}

// fold moves the baseline toward the last completed minute's mean before a new minute starts.
func (s *series) fold(minute int64) {
	previous := s.buckets[(minute-1+Window)%Window]
	if previous.minute != minute-1 || previous.ops < minOps {
		return
	}
	mean := (previous.latencyTotal - previous.worst) / time.Duration(previous.ops-1)
	switch {
	case s.baseline == 0:
		s.baseline = mean
	case mean < s.baseline:
		s.baseline += time.Duration(fallWeight * float64(mean-s.baseline))
	default:
		s.baseline += time.Duration(riseWeight * float64(mean-s.baseline))
	}
}

// Latency records one operation that answers without moving much data: a stat, a directory read, a request's first byte.
func (r *Recorder) Latency(kind Kind, key string, d time.Duration) {
	if key == "" {
		return
	}
	r.mu.Lock()
	_, b := r.current(kind, key)
	b.ops++
	b.latencyTotal += d
	b.worst = max(b.worst, d)
	r.mu.Unlock()
}

// Transfer records bytes moved and the time spent moving them.
func (r *Recorder) Transfer(kind Kind, key string, bytes int64, d time.Duration) {
	if key == "" || bytes <= 0 {
		return
	}
	r.mu.Lock()
	_, b := r.current(kind, key)
	b.bytes += bytes
	b.busy += d
	r.mu.Unlock()
}

// Point is one minute of a series.
type Point struct {
	Minute  int64 // Unix minute
	Ops     int64
	Latency time.Duration // mean latency; 0 when no operations
	Bytes   int64
}

// Stat is what is known of one series.
type Stat struct {
	Kind Kind
	Key  string
	// Recent is the mean latency over the last two minutes; Baseline what this series normally takes.
	Recent, Baseline time.Duration
	// Degradation is Recent over Baseline, never below 1; 1 when there is too little data to say.
	Degradation float64
	BytesPerSec float64
	OpsPerMin   float64
	History     []Point // oldest first, completed minutes plus the current one
}

// Snapshot returns every series, ordered by kind and key.
func (r *Recorder) Snapshot() []Stat {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats(true)
}

func (r *Recorder) stats(history bool) []Stat {
	now := r.clock.Now().Unix() / 60
	stats := make([]Stat, 0, len(r.series))
	for k, s := range r.series {
		stats = append(stats, s.stat(k, now, history))
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Kind != stats[j].Kind {
			return stats[i].Kind < stats[j].Kind
		}
		return stats[i].Key < stats[j].Key
	})
	return stats
}

func (s *series) stat(k seriesKey, now int64, history bool) Stat {
	stat := Stat{Kind: k.kind, Key: k.key, Baseline: s.baseline, Degradation: 1}
	var recentOps int64
	var recentTotal time.Duration
	for minute := now - Window + 1; minute <= now; minute++ {
		b := s.buckets[minute%Window]
		point := Point{Minute: minute}
		if b.minute == minute {
			point.Ops, point.Bytes = b.ops, b.bytes
			if b.ops > 0 {
				point.Latency = b.latencyTotal / time.Duration(b.ops)
			}
			if minute >= now-1 {
				// One stall in a minute is noise; dropping the worst reading keeps a lone hiccup from reading as load.
				if b.ops > 1 {
					recentOps += b.ops - 1
					recentTotal += b.latencyTotal - b.worst
				}
			}
			if minute == now-1 {
				stat.OpsPerMin = float64(b.ops)
				stat.BytesPerSec = float64(b.bytes) / 60
			}
		}
		if history {
			stat.History = append(stat.History, point)
		}
	}
	if recentOps > 0 {
		stat.Recent = recentTotal / time.Duration(recentOps)
	}
	if recentOps >= minOps && s.baseline > 0 && stat.Recent > s.baseline {
		stat.Degradation = float64(stat.Recent) / float64(s.baseline)
	}
	return stat
}

// Load is the worst degradation among the series of a kind: 1 while everything runs as it normally does.
func (r *Recorder) Load(kind Kind) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	load := 1.0
	for _, stat := range r.stats(false) {
		if stat.Kind == kind && stat.Degradation > load {
			load = stat.Degradation
		}
	}
	return load
}

// LoadOf is the degradation of one series (a root folder's disk, say): 1 while it runs as it normally does or is unknown.
func (r *Recorder) LoadOf(kind Kind, key string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	one, found := r.series[seriesKey{kind, key}]
	if !found {
		return 1
	}
	return max(1, one.stat(seriesKey{kind, key}, r.clock.Now().Unix()/60, false).Degradation)
}
