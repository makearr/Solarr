package transcoder

import (
	"context"
	"slices"
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

// ceilingsOk says whether one more conversion of the lane fits: it counts against its own lane's limit and every later
// one, each of which counts every lane up to itself.
func ceilingsOk(limits map[Lane]int, acquired map[Lane]int, lane Lane) bool {
	return roomIn(limits, acquired, lane) > 0
}

// roomIn is how many more conversions of the lane fit under every ceiling that counts it.
func roomIn(limits map[Lane]int, acquired map[Lane]int, lane Lane) int {
	room, within, started, bounded := 0, 0, false, false
	for _, tier := range LaneOrder {
		within += acquired[tier]
		if tier == lane {
			started = true
		}
		if !started {
			continue
		}
		limit, set := limits[tier]
		if !set {
			continue
		}
		if left := limit - within; !bounded || left < room {
			room, bounded = left, true
		}
	}
	return max(0, room)
}

// Acquire takes a place in the lane; false when it or a later ceiling is full. The release gives it back.
func (s *Slots) Acquire(lane Lane) (release func(), ok bool) {
	limits := s.limitsNow()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ceilingsOk(limits, s.acquired, lane) {
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
	for _, lane := range LaneOrder {
		free[lane] = roomIn(limits, s.acquired, lane)
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

// Govern pauses the youngest processes while a ceiling is exceeded and resumes the oldest paused ones that fit again.
func (s *Slots) Govern() {
	limits := s.limitsNow()
	s.mu.Lock()
	tracked := append([]*trackedProcess(nil), s.tracked...)
	s.mu.Unlock()
	var running, paused []*trackedProcess
	for _, entry := range tracked {
		if entry.process.Paused() {
			paused = append(paused, entry)
		} else {
			running = append(running, entry)
		}
	}
	slices.SortFunc(running, func(a, b *trackedProcess) int { return int(a.order - b.order) })
	for _, tier := range LaneOrder {
		limit, set := limits[tier]
		for set && counted(running, tier) > limit {
			youngest := youngestUpTo(running, tier)
			_ = youngest.process.Pause() // a process that just ended cannot be paused, and needs no pause
			running = slices.DeleteFunc(running, func(entry *trackedProcess) bool { return entry == youngest })
			paused = append(paused, youngest)
		}
	}
	slices.SortFunc(paused, func(a, b *trackedProcess) int { return int(a.order - b.order) })
	for _, entry := range paused {
		running2 := map[Lane]int{}
		for _, other := range running {
			running2[other.lane]++
		}
		if ceilingsOk(limits, running2, entry.lane) {
			_ = entry.process.Resume()
			running = append(running, entry)
		}
	}
}

// counted is how many of the processes run in the lane or an earlier one.
func counted(processes []*trackedProcess, tier Lane) int {
	count := 0
	for _, entry := range processes {
		if laneIndex(entry.lane) <= laneIndex(tier) {
			count++
		}
	}
	return count
}

// youngestUpTo is the newest process in the lane or an earlier one.
func youngestUpTo(processes []*trackedProcess, tier Lane) *trackedProcess {
	var youngest *trackedProcess
	for _, entry := range processes {
		if laneIndex(entry.lane) <= laneIndex(tier) && (youngest == nil || entry.order > youngest.order) {
			youngest = entry
		}
	}
	return youngest
}

func laneIndex(lane Lane) int { return slices.Index(LaneOrder, lane) }

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
