package mediatool

import (
	"errors"
	"math/rand"
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
