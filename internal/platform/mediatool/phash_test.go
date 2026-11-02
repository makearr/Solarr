package mediatool

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
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
