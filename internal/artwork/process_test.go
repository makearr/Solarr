package artwork_test

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"

	xwebp "golang.org/x/image/webp"

	"github.com/makearr/MediaNexus/internal/artwork"
)

// syntheticCover builds a non-trivial image -- a smooth gradient, not a flat
// colour -- so resizing and lossy compression actually have something to do.
func syntheticCover(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r := uint8(128 + 127*math.Sin(float64(x)/23.0))
			g := uint8(128 + 127*math.Cos(float64(y)/31.0))
			b := uint8((x + y) % 256)
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	return img
}

func TestProcessImage_ProducesAllThreeSizesAsValidDecodableWebP(t *testing.T) {
	source := syntheticCover(1600, 1200)

	result, err := artwork.ProcessImage(source)
	if err != nil {
		t.Fatalf("ProcessImage: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("ProcessImage returned %d sizes, want 3", len(result))
	}

	for _, size := range []artwork.Size{artwork.Thumb, artwork.Small, artwork.Full} {
		out, ok := result[size]
		if !ok {
			t.Fatalf("missing size %d in result", size)
		}
		if len(out.Bytes) == 0 {
			t.Fatalf("size %d: empty WebP bytes", size)
		}
		if max(out.Width, out.Height) != int(size) {
			t.Errorf("size %d: longest edge = %dx%d, want the longest side to equal %d", size, out.Width, out.Height, size)
		}

		decoded, err := xwebp.Decode(bytes.NewReader(out.Bytes))
		if err != nil {
			t.Fatalf("size %d: decode back failed, corrupt WebP: %v", size, err)
		}
		bounds := decoded.Bounds()
		if bounds.Dx() != out.Width || bounds.Dy() != out.Height {
			t.Errorf("size %d: decoded bounds %dx%d, want reported %dx%d",
				size, bounds.Dx(), bounds.Dy(), out.Width, out.Height)
		}

		distinct := map[color.RGBA]bool{}
		for _, p := range [][2]int{{0, 0}, {bounds.Dx() / 2, bounds.Dy() / 2}, {bounds.Dx() - 1, bounds.Dy() - 1}} {
			r, g, b, a := decoded.At(p[0], p[1]).RGBA()
			distinct[color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}] = true
		}
		if len(distinct) < 2 {
			t.Errorf("size %d: decoded pixels all identical, output looks blank/corrupt", size)
		}
	}
}

func TestProcessImage_NeverUpscalesASmallSource(t *testing.T) {
	source := syntheticCover(50, 40)

	result, err := artwork.ProcessImage(source)
	if err != nil {
		t.Fatalf("ProcessImage: %v", err)
	}
	for _, size := range []artwork.Size{artwork.Thumb, artwork.Small, artwork.Full} {
		out := result[size]
		if out.Width != 50 || out.Height != 40 {
			t.Errorf("size %d: dims = %dx%d, want the untouched source 50x40 since it is smaller than every target", size, out.Width, out.Height)
		}
	}
}
