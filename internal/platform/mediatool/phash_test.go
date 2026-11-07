package mediatool

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"
	"time"
)

func syntheticFrame(seed int64, blobs int) []byte {
	random := rand.New(rand.NewSource(seed))
	frame := make([]byte, phashSide*phashSide)
	for range blobs {
		cx, cy, radius := random.Intn(phashSide), random.Intn(phashSide), 3+random.Intn(8)
		level := byte(random.Intn(255))
		for y := range phashSide {
			for x := range phashSide {
				if (x-cx)*(x-cx)+(y-cy)*(y-cy) < radius*radius {
					frame[y*phashSide+x] = level
				}
			}
		}
	}
	return frame
}

func regraded(frame []byte, seed int64) []byte {
	random := rand.New(rand.NewSource(seed))
	out := make([]byte, len(frame))
	for index, value := range frame {
		shifted := int(value)*9/10 + 12 + random.Intn(7) - 3
		out[index] = byte(max(0, min(255, shifted)))
	}
	return out
}

func TestPerceptualHashOfFrames_SameCutReencodedIsNear(t *testing.T) {
	frames := [][]byte{syntheticFrame(1, 9), syntheticFrame(2, 9), syntheticFrame(3, 9)}
	again := [][]byte{regraded(frames[0], 7), regraded(frames[1], 8), regraded(frames[2], 9)}
	a, err := PerceptualHashOfFrames(frames)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PerceptualHashOfFrames(again)
	if err != nil {
		t.Fatal(err)
	}
	if got := PerceptualDistance(a, b); got > 8 {
		t.Errorf("re-graded copy is %d bits away, want <= 8", got)
	}
}

func TestPerceptualHashOfFrames_DifferentVideosAreFar(t *testing.T) {
	a, _ := PerceptualHashOfFrames([][]byte{syntheticFrame(11, 9), syntheticFrame(12, 9)})
	b, _ := PerceptualHashOfFrames([][]byte{syntheticFrame(21, 9), syntheticFrame(22, 9)})
	if got := PerceptualDistance(a, b); got < 16 {
		t.Errorf("different videos are %d bits apart, want >= 16", got)
	}
}

func TestPerceptualHashOfFrames_Rejects(t *testing.T) {
	if _, err := PerceptualHashOfFrames(nil); !errors.Is(err, ErrNoFrames) {
		t.Errorf("no frames: err = %v", err)
	}
	if _, err := PerceptualHashOfFrames([][]byte{{1, 2, 3}}); err == nil {
		t.Error("short frame accepted")
	}
}

// scene is a picture that is a function of position and time, so it can be rendered at any size and moment: soft blobs
// drifting slowly, the way a shot with no cut changes between neighbouring frames.
type scene struct{ blobs []blob }

type blob struct{ x, y, dx, dy, radius, level float64 }

func newScene(seed int64) scene { return newMovingScene(seed, 1) }

// newMovingScene drifts its blobs speed times as fast as a slow shot does.
func newMovingScene(seed int64, speed float64) scene {
	random := rand.New(rand.NewSource(seed))
	var blobs []blob
	for range 10 {
		blobs = append(blobs, blob{
			x: random.Float64(), y: random.Float64(), dx: (random.Float64() - 0.5) * 0.04 * speed, dy: (random.Float64() - 0.5) * 0.04 * speed,
			radius: 0.08 + random.Float64()*0.2, level: 40 + random.Float64()*200,
		})
	}
	return scene{blobs: blobs}
}

// at is the grey at (u, v) in the unit square, seconds in.
func (s scene) at(u, v, seconds float64) float64 {
	grey := 90.0
	for _, b := range s.blobs {
		dx, dy := u-wrap(b.x+b.dx*seconds), v-wrap(b.y+b.dy*seconds)
		if dx*dx+dy*dy < b.radius*b.radius {
			grey = b.level
		}
	}
	return grey
}

func wrap(position float64) float64 { return position - math.Floor(position) }

// view is how one copy of a video shows a scene: the picture occupies a rectangle of the FrameSide frame (the rest is
// black bars) and is regraded and noised the way a re-encode would.
type view struct {
	left, top, right, bottom float64
	noise                    int64
}

var (
	plain        = view{0, 0, 1, 1, 1}
	letterboxed  = view{0, 0.125, 1, 0.875, 2}
	pillarboxed  = view{0.125, 0, 0.875, 1, 3}
	stretched169 = view{0, 0, 1, 1, 4}
)

func (v view) frame(s scene, seconds float64) []byte {
	random := rand.New(rand.NewSource(v.noise))
	frame := make([]byte, FrameSide*FrameSide)
	for y := range FrameSide {
		for x := range FrameSide {
			px, py := (float64(x)+0.5)/FrameSide, (float64(y)+0.5)/FrameSide
			grey := 6.0
			if px >= v.left && px < v.right && py >= v.top && py < v.bottom {
				grey = s.at((px-v.left)/(v.right-v.left), (py-v.top)/(v.bottom-v.top), seconds)*0.92 + 8
			}
			frame[y*FrameSide+x] = byte(max(0, min(255, int(grey)+random.Intn(5)-2)))
		}
	}
	return frame
}

// windows reads windowFrames frames windowStep apart at each of seven moments, offset by shift seconds.
func (v view) windows(s scene, shift float64) [][][]byte {
	out := make([][][]byte, phashSamples)
	for slot := range out {
		start := float64(slot+1)*10 + shift
		for k := range windowFrames {
			out[slot] = append(out[slot], v.frame(s, start+float64(k)*windowStep.Seconds()))
		}
	}
	return out
}

func hashOf(t *testing.T, windows [][][]byte) VideoHash {
	t.Helper()
	hash, err := HashOfWindows(windows)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// slotsMatching counts the moments at which some frame of one hash is within bits of some frame of the other.
func slotsMatching(a, b VideoHash, bits int) int {
	matched := 0
	for slot := range phashSamples {
		near := false
		for _, first := range a.Frames {
			for _, second := range b.Frames {
				if first.Slot == slot && second.Slot == slot && PerceptualDistance(first.Hash, second.Hash) <= bits {
					near = true
				}
			}
		}
		if near {
			matched++
		}
	}
	return matched
}

func TestHashOfWindows_TheSameContentInAnotherFrameStillMatches(t *testing.T) {
	picture := newScene(7)
	reference := hashOf(t, plain.windows(picture, 0))
	for _, test := range []struct {
		name  string
		hash  VideoHash
		slots int
	}{
		{"letterboxed", hashOf(t, letterboxed.windows(picture, 0)), 7},
		{"pillarboxed", hashOf(t, pillarboxed.windows(picture, 0)), 7},
		{"re-encoded with other noise", hashOf(t, stretched169.windows(picture, 0)), 7},
		{"two frames later at 24 fps", hashOf(t, plain.windows(picture, 2.0/24)), 7},
		{"three frames earlier at 25 fps", hashOf(t, letterboxed.windows(picture, -3.0/25)), 7},
	} {
		if got := slotsMatching(reference, test.hash, 8); got < test.slots {
			t.Errorf("%s: %d of %d moments match within 8 bits", test.name, got, test.slots)
		}
		if got := PerceptualDistance(reference.Mean, test.hash.Mean); got > 8 {
			t.Errorf("%s: mean hashes %d bits apart, want <= 8", test.name, got)
		}
	}
}

// In motion one frame of a moment is not the frame a copy shifted by a few frames shows; the neighbours in the window are.
func TestHashOfWindows_AShiftInMotionStillFindsAMatchingFrame(t *testing.T) {
	picture := newMovingScene(12, 12)
	reference := hashOf(t, plain.windows(picture, 0))
	shifted := hashOf(t, stretched169.windows(picture, 0.17))
	if got := slotsMatching(reference, shifted, 8); got < 6 {
		t.Errorf("shifted 0.17 s in motion: %d of 7 moments match within 8 bits, want >= 6", got)
	}
}

func TestHashOfWindows_AnotherPictureDoesNotMatch(t *testing.T) {
	reference := hashOf(t, plain.windows(newScene(7), 0))
	other := hashOf(t, plain.windows(newScene(8), 0))
	if got := slotsMatching(reference, other, 8); got > 1 {
		t.Errorf("another picture: %d of 7 moments match within 8 bits, want at most 1", got)
	}
	if got := PerceptualDistance(reference.Mean, other.Mean); got < 12 {
		t.Errorf("another picture: mean hashes %d bits apart, want >= 12", got)
	}
}

// Without the crop a letterboxed copy is a different picture: this is the guard that the bars come off.
func TestHashOfWindows_BarsAreCroppedOnlyWhenTheyAreBars(t *testing.T) {
	picture := newScene(9)
	plainWindows := plain.windows(picture, 0)
	got := commonBars(flatten(letterboxed.windows(picture, 0)))
	if want := (bars{top: 17, bottom: 17}); got != want {
		t.Errorf("letterbox bars = %+v, want %+v", got, want)
	}
	if got := commonBars(flatten(plainWindows)); got != (bars{}) {
		t.Errorf("a picture to its edges: bars = %+v, want none", got)
	}
	subtitled := letterboxed.windows(picture, 0)
	// A full-width strip (a station logo bar) on one moment, a line of subtitles on another.
	for slot, width := range map[int][2]int{1: {0, FrameSide}, 4: {40, 90}} {
		for _, frame := range subtitled[slot] {
			for y := 112; y < 120; y++ {
				for x := width[0]; x < width[1]; x++ {
					frame[y*FrameSide+x] = 235
				}
			}
		}
	}
	if got := commonBars(flatten(subtitled)); got != (bars{top: 17, bottom: 17}) {
		t.Errorf("a strip and subtitles in the bar on two moments: bars = %+v, want the bars kept", got)
	}
	dark := make([]byte, FrameSide*FrameSide)
	for index := range dark {
		dark[index] = 10
	}
	if got := barsOf(dark); got.top != barLimit || got.left != barLimit {
		t.Errorf("an all-dark frame: bars = %+v, want the limit %d each side", got, barLimit)
	}
}

func flatten(windows [][][]byte) [][]byte {
	var out [][]byte
	for _, window := range windows {
		out = append(out, window...)
	}
	return out
}

// A cut inside a window keeps the longer side, so the frames hashed all belong to one shot.
func TestHashOfWindows_AWindowKeepsOnlyItsLongestShot(t *testing.T) {
	first, second := newScene(21), newScene(22)
	windows := plain.windows(first, 0)
	for k := 3; k < windowFrames; k++ {
		windows[0][k] = plain.frame(second, float64(k)*windowStep.Seconds())
	}
	cut := hashOf(t, windows)
	before := hashOf(t, plain.windows(first, 0))
	slotZero := func(hash VideoHash) (hashes []uint64) {
		for _, frame := range hash.Frames {
			if frame.Slot == 0 {
				hashes = append(hashes, frame.Hash)
			}
		}
		return hashes
	}
	got := slotZero(cut)
	if len(got) == 0 || len(got) > 3 {
		t.Fatalf("slot 0 of a window cut after 3 frames kept %d hashes, want 1 to 3", len(got))
	}
	for _, hash := range got {
		if !slices.Contains(slotZero(before), hash) {
			t.Errorf("slot 0 kept hash %#x of the second shot", hash)
		}
	}
}

func TestHashOfWindows_SkipsUnreadAndFlatMoments(t *testing.T) {
	picture := newScene(31)
	windows := plain.windows(picture, 0)
	windows[1] = nil
	flat := make([]byte, FrameSide*FrameSide)
	for index := range flat {
		flat[index] = 120
	}
	windows[2] = [][]byte{flat}
	got := hashOf(t, windows)
	for _, frame := range got.Frames {
		if frame.Slot == 1 || frame.Slot == 2 {
			t.Errorf("slot %d has a hash; it was unread or flat", frame.Slot)
		}
	}
	if len(got.Frames) == 0 {
		t.Error("no frames hashed")
	}
	if _, err := HashOfWindows([][][]byte{nil, nil}); !errors.Is(err, ErrNoFrames) {
		t.Errorf("nothing read: err = %v", err)
	}
	if _, err := HashOfWindows([][][]byte{{flat}}); !errors.Is(err, ErrNoFrames) {
		t.Errorf("only flat frames: err = %v", err)
	}
	if _, err := HashOfWindows([][][]byte{{{1, 2, 3}}}); err == nil {
		t.Error("a short frame was accepted")
	}
}

// The same scenes in another order average to one mean image, which is how one show's episodes looked alike.
func TestPerceptualHashOfFrames_ReorderedScenesShareTheMeanButNotTheFrames(t *testing.T) {
	scenes := [][]byte{syntheticFrame(41, 9), syntheticFrame(42, 9), syntheticFrame(43, 9)}
	reordered := [][]byte{scenes[1], scenes[2], scenes[0]}
	a, _ := PerceptualHashOfFrames(scenes)
	b, _ := PerceptualHashOfFrames(reordered)
	if got := PerceptualDistance(a, b); got != 0 {
		t.Errorf("mean hashes are %d bits apart, want 0", got)
	}
	for index := range scenes {
		first, _ := frameHash(scenes[index])
		second, _ := frameHash(reordered[index])
		if got := PerceptualDistance(first, second); got < 16 {
			t.Errorf("slot %d: %d bits apart, want >= 16", index, got)
		}
	}
}

type frameRunner struct {
	hardwareFails bool
	calls         [][]string
}

func (*frameRunner) Lookup(string) error { return nil }

func (r *frameRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, args)
	if r.hardwareFails && slices.Contains(args, "-hwaccel") {
		return nil, errors.New("vaapi failed")
	}
	return make([]byte, windowFrames*FrameSide*FrameSide*3), nil
}

func TestWindowAt_DecodesOnTheGPUAndFallsBackToTheProcessor(t *testing.T) {
	hardware := func() []string { return []string{"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128"} }
	good := &frameRunner{}
	if _, err := New(good, 1).WithHardwareDecode(hardware).windowAt(t.Context(), "/m/a.mkv", time.Second, pictureSize{FrameSide, FrameSide}); err != nil {
		t.Fatal(err)
	}
	if len(good.calls) != 1 || !slices.Contains(good.calls[0], "-hwaccel") {
		t.Fatalf("calls = %v; want one call with -hwaccel", good.calls)
	}
	bad := &frameRunner{hardwareFails: true}
	if _, err := New(bad, 1).WithHardwareDecode(hardware).windowAt(t.Context(), "/m/a.mkv", time.Second, pictureSize{FrameSide, FrameSide}); err != nil {
		t.Fatal(err)
	}
	if len(bad.calls) != 2 || slices.Contains(bad.calls[1], "-hwaccel") {
		t.Fatalf("calls = %v; want the hardware try, then a processor call", bad.calls)
	}
}
