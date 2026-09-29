package artwork

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

	webp "github.com/mayahiro/go-webp"
)

// encodeQuality is a common sane default for photographic lossy WebP --
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

// EncodeWebP writes img as lossy VP8 WebP. Cover art is photographic, so
// lossy is the right trade-off; lossless would defeat much of the point of
// choosing WebP here.
func EncodeWebP(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	options := &webp.Options{Compression: webp.CompressionLossy, Quality: encodeQuality}
	if err := webp.Encode(&buf, img, options); err != nil {
		return nil, fmt.Errorf("artwork: encode webp: %w", err)
	}
	return buf.Bytes(), nil
}
