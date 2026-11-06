package mediatool

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
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

func TestHashOfFrames_SkipsUnreadAndFlatSlotsButAveragesEveryReadFrame(t *testing.T) {
	flat := make([]byte, phashSide*phashSide)
	for index := range flat {
		flat[index] = 16
	}
	first, third := syntheticFrame(31, 9), syntheticFrame(33, 9)
	got, err := HashOfFrames([][]byte{first, nil, third, flat})
	if err != nil {
		t.Fatal(err)
	}
	mean, _ := PerceptualHashOfFrames([][]byte{first, third, flat})
	firstHash, _ := frameHash(first)
	thirdHash, _ := frameHash(third)
	want := VideoHash{Mean: mean, Frames: []FrameHash{{Slot: 0, Hash: firstHash}, {Slot: 2, Hash: thirdHash}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HashOfFrames = %+v, want %+v", got, want)
	}
	if _, err := HashOfFrames([][]byte{nil, nil}); !errors.Is(err, ErrNoFrames) {
		t.Errorf("nothing read: err = %v", err)
	}
}

// The same scenes in another order average to one mean image, which is how one show's episodes looked alike.
func TestHashOfFrames_ReorderedScenesShareTheMeanButNotTheFrames(t *testing.T) {
	scenes := [][]byte{syntheticFrame(41, 9), syntheticFrame(42, 9), syntheticFrame(43, 9)}
	a, _ := HashOfFrames(scenes)
	b, _ := HashOfFrames([][]byte{scenes[1], scenes[2], scenes[0]})
	if got := PerceptualDistance(a.Mean, b.Mean); got != 0 {
		t.Errorf("mean hashes are %d bits apart, want 0", got)
	}
	for index := range a.Frames {
		if got := PerceptualDistance(a.Frames[index].Hash, b.Frames[index].Hash); got < 16 {
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
	return make([]byte, phashSide*phashSide), nil
}

func TestFrameAt_DecodesOnTheGPUAndFallsBackToTheProcessor(t *testing.T) {
	hardware := func() []string { return []string{"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128"} }
	good := &frameRunner{}
	if _, err := New(good, 1).WithHardwareDecode(hardware).frameAt(t.Context(), "/m/a.mkv", time.Second); err != nil {
		t.Fatal(err)
	}
	if len(good.calls) != 1 || !slices.Contains(good.calls[0], "-hwaccel") {
		t.Fatalf("calls = %v; want one call with -hwaccel", good.calls)
	}
	bad := &frameRunner{hardwareFails: true}
	if _, err := New(bad, 1).WithHardwareDecode(hardware).frameAt(t.Context(), "/m/a.mkv", time.Second); err != nil {
		t.Fatal(err)
	}
	if len(bad.calls) != 2 || slices.Contains(bad.calls[1], "-hwaccel") {
		t.Fatalf("calls = %v; want the hardware try, then a processor call", bad.calls)
	}
}
