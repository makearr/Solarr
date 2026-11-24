package transcoder

import (
	"context"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// governEvery is how often the lane limits are compared with what runs.
const governEvery = 5 * time.Second

// Slots bounds how many conversions run per lane; the limits are read on every call so a settings change applies at once.
type Slots struct {
	limits func(playing bool) map[Lane]int
	active func() int
	clock  clock.Clock

	mu       sync.Mutex
	acquired map[Lane]int
	tracked  []*trackedProcess
	order    int64
}

type trackedProcess struct {
	lane    Lane
	process transcode.Process
	order   int64
}

// NewSlots builds it; limits answers the lane limits for normal use or while a viewer is converting, and active how many viewers are.
func NewSlots(limits func(playing bool) map[Lane]int, active func() int, cl clock.Clock) *Slots {
	return &Slots{limits: limits, active: active, clock: cl, acquired: map[Lane]int{}}
}

func (s *Slots) limitsNow() map[Lane]int { return s.limits(s.active() > 0) }

// Acquire takes a place in the lane; false when the lane is full. The release gives it back.
func (s *Slots) Acquire(lane Lane) (release func(), ok bool) {
	limit := s.limitsNow()[lane]
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acquired[lane] >= limit {
		return nil, false
	}
	s.acquired[lane]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.acquired[lane]--
		})
	}, true
}

// Free is how many more files each lane may start.
func (s *Slots) Free() map[Lane]int {
	limits := s.limitsNow()
	s.mu.Lock()
	defer s.mu.Unlock()
	free := map[Lane]int{}
	for _, lane := range []Lane{LaneCPU, LaneGPU, LaneAudio} {
		free[lane] = max(0, limits[lane]-s.acquired[lane])
	}
	return free
}

// Track lets the governor pause and resume a running process; the returned function forgets it.
func (s *Slots) Track(lane Lane, process transcode.Process) (untrack func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order++
	entry := &trackedProcess{lane: lane, process: process, order: s.order}
	s.tracked = append(s.tracked, entry)
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, other := range s.tracked {
			if other == entry {
				s.tracked = append(s.tracked[:i], s.tracked[i+1:]...)
				return
			}
		}
	}
}

// Govern pauses the youngest processes of a lane that runs more than its limit and resumes the oldest paused ones when it runs fewer.
func (s *Slots) Govern() {
	limits := s.limitsNow()
	s.mu.Lock()
	tracked := append([]*trackedProcess(nil), s.tracked...)
	s.mu.Unlock()
	for _, lane := range []Lane{LaneCPU, LaneGPU, LaneAudio} {
		var running, paused []*trackedProcess
		for _, entry := range tracked {
			switch {
			case entry.lane != lane:
			case entry.process.Paused():
				paused = append(paused, entry)
			default:
				running = append(running, entry)
			}
		}
		limit := limits[lane]
		for len(running) > limit {
			youngest := running[len(running)-1]
			_ = youngest.process.Pause() // a process that just ended cannot be paused, and needs no pause
			running = running[:len(running)-1]
		}
		for len(running) < limit && len(paused) > 0 {
			_ = paused[0].process.Resume()
			running = append(running, paused[0])
			paused = paused[1:]
		}
	}
}

// Run governs until the context ends.
func (s *Slots) Run(ctx context.Context) {
	ticker := s.clock.NewTicker(governEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			s.Govern()
		}
	}
}
