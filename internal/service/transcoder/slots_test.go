package transcoder_test

import (
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

type fakeProcess struct {
	mu     sync.Mutex
	paused bool
}

func (*fakeProcess) Wait() error { return nil }
func (p *fakeProcess) Pause() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = true
	return nil
}
func (p *fakeProcess) Resume() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = false
	return nil
}
func (p *fakeProcess) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

type limitsBox struct {
	mu      sync.Mutex
	normal  map[transcoder.Lane]int
	playing map[transcoder.Lane]int
	viewers int
}

func (b *limitsBox) set(normal int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.normal[transcoder.LaneCPU] = normal
}

func (b *limitsBox) slots() *transcoder.Slots {
	return transcoder.NewSlots(func(playing bool) map[transcoder.Lane]int {
		b.mu.Lock()
		defer b.mu.Unlock()
		if playing {
			return b.playing
		}
		return b.normal
	}, func() int { b.mu.Lock(); defer b.mu.Unlock(); return b.viewers }, clock.NewFakeAtEpoch())
}

func newBox() *limitsBox {
	return &limitsBox{normal: map[transcoder.Lane]int{transcoder.LaneCPU: 2, transcoder.LaneGPU: 1, transcoder.LaneAudio: 4}, playing: map[transcoder.Lane]int{transcoder.LaneCPU: 0, transcoder.LaneGPU: 1, transcoder.LaneAudio: 2}}
}

func TestSlots_AcquireStopsAtTheLimitAndReleaseGivesTheSlotBackOnce(t *testing.T) {
	slots := newBox().slots()
	first, ok1 := slots.Acquire(transcoder.LaneCPU)
	_, ok2 := slots.Acquire(transcoder.LaneCPU)
	if _, ok3 := slots.Acquire(transcoder.LaneCPU); !ok1 || !ok2 || ok3 {
		t.Fatalf("acquire = %v %v %v, want two of two then refused", ok1, ok2, ok3)
	}
	if free := slots.Free(); free[transcoder.LaneCPU] != 0 || free[transcoder.LaneGPU] != 1 || free[transcoder.LaneAudio] != 4 {
		t.Errorf("free = %v", free)
	}
	first()
	first()
	if free := slots.Free(); free[transcoder.LaneCPU] != 1 {
		t.Errorf("a double release freed %d slots", free[transcoder.LaneCPU])
	}
}

func TestSlots_GovernPausesTheYoungestOverTheLimitAndResumesTheOldestWhenItGrows(t *testing.T) {
	box := newBox()
	slots := box.slots()
	oldest, middle, youngest := &fakeProcess{}, &fakeProcess{}, &fakeProcess{}
	for _, process := range []*fakeProcess{oldest, middle, youngest} {
		slots.Track(transcoder.LaneCPU, process)
	}
	box.set(1)
	slots.Govern()
	if oldest.Paused() || !middle.Paused() || !youngest.Paused() {
		t.Fatalf("limit 1: paused = %v %v %v, want only the oldest running", oldest.Paused(), middle.Paused(), youngest.Paused())
	}
	box.set(2)
	slots.Govern()
	if oldest.Paused() || middle.Paused() || !youngest.Paused() {
		t.Fatalf("limit 2: paused = %v %v %v, want the oldest paused one resumed", oldest.Paused(), middle.Paused(), youngest.Paused())
	}
	box.set(0)
	slots.Govern()
	if !oldest.Paused() || !middle.Paused() || !youngest.Paused() {
		t.Error("limit 0 must pause all")
	}
}

func TestSlots_AViewerSwitchesToThePlaybackLimitsAndUntrackForgetsAProcess(t *testing.T) {
	box := newBox()
	slots := box.slots()
	one, two := &fakeProcess{}, &fakeProcess{}
	slots.Track(transcoder.LaneCPU, one)
	forget := slots.Track(transcoder.LaneCPU, two)
	box.mu.Lock()
	box.viewers = 1
	box.mu.Unlock()
	slots.Govern()
	if !one.Paused() || !two.Paused() {
		t.Fatal("while a viewer converts the cpu limit is 0")
	}
	if _, ok := slots.Acquire(transcoder.LaneCPU); ok {
		t.Error("acquired a cpu slot at playback limit 0")
	}
	box.mu.Lock()
	box.viewers = 0
	box.mu.Unlock()
	forget()
	slots.Govern()
	if one.Paused() || !two.Paused() {
		t.Errorf("after the viewer left: paused = %v %v; want the tracked one resumed and the forgotten one left alone", one.Paused(), two.Paused())
	}
}

func TestSlots_RunGovernsOnTheClockTick(t *testing.T) {
	box := newBox()
	fake := clock.NewFakeAtEpoch()
	slots := transcoder.NewSlots(func(bool) map[transcoder.Lane]int { return box.normal }, func() int { return 0 }, fake)
	process := &fakeProcess{}
	slots.Track(transcoder.LaneCPU, process)
	box.set(0)
	done := make(chan struct{})
	go func() { slots.Run(t.Context()); close(done) }()
	fake.BlockUntilWaiters(1)
	fake.Advance(5 * time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for !process.Paused() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !process.Paused() {
		t.Error("the tick did not govern")
	}
}
