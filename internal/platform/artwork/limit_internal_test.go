package artwork

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"runtime"
	"testing"

	"golang.org/x/image/draw"
)

func TestProcessImageWaitingForASlotReturnsWhenItsContextEnds(t *testing.T) {
	for range cap(slots) {
		slots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(slots) {
			<-slots
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := ProcessImage(ctx, image.NewRGBA(image.Rect(0, 0, 10, 10))); !errors.Is(err, context.Canceled) {
		t.Errorf("ProcessImage with every slot taken = %v, want context.Canceled", err)
	}
}

func TestProcessWaitsForASlotLikeProcessImageDoes(t *testing.T) {
	for range cap(slots) {
		slots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(slots) {
			<-slots
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := Process(ctx, []byte("not even a picture")); !errors.Is(err, context.Canceled) {
		t.Errorf("Process with every slot taken = %v, want context.Canceled before any decoding", err)
	}
}

func TestSlotCountIsHalfTheCoresButAtLeastTwo(t *testing.T) {
	if got, want := cap(slots), max(2, runtime.GOMAXPROCS(0)/2); got != want {
		t.Errorf("slots = %d, want %d", got, want)
	}
}

// psnr is the peak signal to noise ratio in dB of two same-sized pictures, over the red, green and blue samples.
func psnr(a, b image.Image) float64 {
	bounds := a.Bounds()
	var sum float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			for _, pair := range [][2]uint32{{ar, br}, {ag, bg}, {ab, bb}} {
				diff := float64(pair[0]>>8) - float64(pair[1]>>8)
				sum += diff * diff
			}
		}
	}
	mean := sum / float64(bounds.Dx()*bounds.Dy()*3)
	if mean == 0 {
		return 100
	}
	return 10 * math.Log10(255*255/mean)
}

// The cascade and the coarse first step trade a little exactness for time; the smallest size must stay close to a
// direct resize of the original.
func TestCascadeAndCoarseStepStayCloseToADirectResize(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for y := range 2000 {
		for x := range 3000 {
			source.Set(x, y, color.RGBA{R: uint8(x * 255 / 3000), G: uint8(y * 255 / 2000), B: uint8(127 + 120*math.Sin(float64(x+y)/150)), A: 255})
		}
	}
	sizesMade, err := ProcessImage(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []Size{Thumb, Small, Full} {
		made, err := Decode(sizesMade[size].Bytes)
		if err != nil {
			t.Fatalf("decode %d: %v", size, err)
		}
		direct := draw2Direct(source, int(size))
		if made.Bounds() != direct.Bounds() {
			t.Fatalf("size %d bounds = %v, want %v", size, made.Bounds(), direct.Bounds())
		}
		if got := psnr(made, direct); got < 26 {
			t.Errorf("size %d PSNR against a direct resize = %.1f dB, want at least 26", size, got)
		}
	}
}

// draw2Direct resizes with the exact filter and no coarse step or cascade.
func draw2Direct(source image.Image, edge int) image.Image {
	bounds := source.Bounds()
	width, height := edge, bounds.Dy()*edge/bounds.Dx()
	if bounds.Dy() > bounds.Dx() {
		width, height = bounds.Dx()*edge/bounds.Dy(), edge
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Src, nil)
	return target
}
