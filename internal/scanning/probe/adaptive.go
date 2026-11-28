package probe

import (
	"context"
	"sync"
)

// backoffAbove is the disk degradation at which a root takes fewer files at once; it is the dispatcher's own figure.
const backoffAbove = 1.5

// adaptiveLimit is the number of files read at once from one root: it starts at one, grows by one for each window of
// files that finished while the root's latency held, halves when the root runs slower than it normally does, and
// never passes the configured parallelism.
type adaptiveLimit struct {
	mu       sync.Mutex
	changed  *sync.Cond
	max      int
	allowed  int
	running  int
	window   int
	pressure func() float64
}

func newAdaptiveLimit(ceiling int, pressure func() float64) *adaptiveLimit {
	limit := &adaptiveLimit{max: ceiling, allowed: 1, pressure: pressure}
	limit.changed = sync.NewCond(&limit.mu)
	return limit
}

// acquire waits for a slot; false when ctx ended first.
func (l *adaptiveLimit) acquire(ctx context.Context) bool {
	stop := context.AfterFunc(ctx, func() {
		l.mu.Lock()
		l.changed.Broadcast()
		l.mu.Unlock()
	})
	defer stop()
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.running >= l.allowed {
		if ctx.Err() != nil {
			return false
		}
		l.changed.Wait()
	}
	l.running++
	return ctx.Err() == nil
}

// release frees a slot and adapts the limit to the root's latency.
func (l *adaptiveLimit) release() {
	load := 1.0
	if l.pressure != nil {
		load = l.pressure()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running--
	switch {
	case load > backoffAbove:
		l.allowed = max(1, l.allowed/2)
		l.window = 0
	case l.allowed < l.max:
		l.window++
		if l.window >= l.allowed {
			l.allowed++
			l.window = 0
		}
	}
	l.changed.Broadcast()
}

// adaptiveLimits are the per-root limits of one sweep.
type adaptiveLimits struct {
	mu       sync.Mutex
	byRoot   map[int64]*adaptiveLimit
	max      int
	pressure func(rootPath string) float64
}

func newAdaptiveLimits(ceiling int, pressure func(rootPath string) float64) *adaptiveLimits {
	return &adaptiveLimits{byRoot: map[int64]*adaptiveLimit{}, max: ceiling, pressure: pressure}
}

// of is the limit of one root, made on first use.
func (l *adaptiveLimits) of(rootID int64, rootPath string) *adaptiveLimit {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit, found := l.byRoot[rootID]; found {
		return limit
	}
	var pressure func() float64
	if l.pressure != nil {
		pressure = func() float64 { return l.pressure(rootPath) }
	}
	limit := newAdaptiveLimit(l.max, pressure)
	l.byRoot[rootID] = limit
	return limit
}
