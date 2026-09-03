package artwork

import "image"

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

// Image is one resized-and-encoded output: WebP bytes plus the pixel
// dimensions actually produced, which may be smaller than Size since
// ResizeLongestEdge never upscales.
type Image struct {
	Bytes  []byte
	Width  int
	Height int
}

// Process decodes source and returns it resized and WebP-encoded at Thumb,
// Small and Full -- the whole pipeline a caller needs to populate the
// artwork table's three cache_key sizes from one fetched image.
func Process(source []byte) (map[Size]Image, error) {
	img, err := Decode(source)
	if err != nil {
		return nil, err
	}
	return ProcessImage(img)
}

// ProcessImage is Process without the decode step, for a caller that already
// has an image.Image.
func ProcessImage(img image.Image) (map[Size]Image, error) {
	result := make(map[Size]Image, len(sizes))
	for _, size := range sizes {
		resized := ResizeLongestEdge(img, int(size))
		encoded, err := EncodeWebP(resized)
		if err != nil {
			return nil, err
		}
		bounds := resized.Bounds()
		result[size] = Image{Bytes: encoded, Width: bounds.Dx(), Height: bounds.Dy()}
	}
	return result, nil
}
