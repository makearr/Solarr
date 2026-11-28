package artwork

import (
	"context"
	"image"
	"runtime"
)

// Size is one of section 21.6's three fixed longest-edge targets.
type Size int

// Thumb, Small and Full are section 21.6's fixed longest-edge targets, in
// pixels: grids request Thumb, detail views Small, the lightbox Full.
const (
	Thumb Size = 160
	Small Size = 400
	Full  Size = 1000
)

// sizes is the fixed set Process produces, grids/detail/lightbox each request
// one (section 21.6).
var sizes = [...]Size{Thumb, Small, Full}

// Image is one resized-and-encoded output: bytes in the stored format plus the pixel
// dimensions actually produced, which may be smaller than Size since
// ResizeLongestEdge never upscales.
type Image struct {
	Bytes  []byte
	Width  int
	Height int
}

// slots bounds the pictures decoded and encoded at once across the whole process: each costs about a second of CPU and
// a decoded copy in memory, so a wall of uncached pictures queues instead of taking every core.
var slots = make(chan struct{}, max(2, runtime.GOMAXPROCS(0)/2))

// acquire waits for a slot; a caller whose ctx ends while waiting returns at once.
func acquire(ctx context.Context) (release func(), err error) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Process decodes source and returns it resized and encoded at Thumb,
// Small and Full -- the whole pipeline a caller needs to populate the
// artwork table's three cache_key sizes from one fetched image.
func Process(ctx context.Context, source []byte) (map[Size]Image, error) {
	release, err := acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	img, err := Decode(source)
	if err != nil {
		return nil, err
	}
	return processImage(img)
}

// ProcessImage is Process without the decode step, for a caller that already
// has an image.Image.
func ProcessImage(ctx context.Context, img image.Image) (map[Size]Image, error) {
	release, err := acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return processImage(img)
}

// processImage cascades: Small is resized from Full and Thumb from Small, which costs a third less than three resizes
// of the original.
func processImage(img image.Image) (map[Size]Image, error) {
	result := make(map[Size]Image, len(sizes))
	for index := len(sizes) - 1; index >= 0; index-- {
		size := sizes[index]
		img = ResizeLongestEdge(img, edgeOf(size))
		encoded, err := Encode(img)
		if err != nil {
			return nil, err
		}
		bounds := img.Bounds()
		result[size] = Image{Bytes: encoded, Width: bounds.Dx(), Height: bounds.Dy()}
	}
	return result, nil
}
