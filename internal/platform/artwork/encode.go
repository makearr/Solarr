package artwork

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// encodeQuality is a common sane default for photographic lossy WebP and JPEG --
// visually indistinguishable from the source at a fraction of PNG's size.
const encodeQuality = 82

// jpegQuality keeps an embedded 1000px cover near 150 KB per file.
const jpegQuality = 88

// EncodeJPEG writes img as JPEG, the picture format every audio player reads from a file's tags.
func EncodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("artwork: encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// EncodeJPEGAt is EncodeJPEG at a chosen quality, for the pictures of video previews.
func EncodeJPEGAt(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("artwork: encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}
