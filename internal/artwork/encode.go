package artwork

import (
	"bytes"
	"fmt"
	"image"

	webp "github.com/mayahiro/go-webp"
)

// encodeQuality is a common sane default for photographic lossy WebP --
// visually indistinguishable from the source at a fraction of PNG's size.
const encodeQuality = 82

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
