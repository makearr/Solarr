package artwork

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder with image.Decode
	_ "image/png"  // registers the PNG decoder with image.Decode

	_ "golang.org/x/image/webp" // registers the WebP decoder: the cache's own covers are WebP
)

// maxDecodePixels is far above any real cover (a 6000x6000 scan is 36 million) and far below what exhausts memory.
const maxDecodePixels = 100_000_000

// Decode reads whatever a provider actually sends -- Cover Art Archive serves
// JPEG and PNG in practice, so both decoders are registered above.
func Decode(source []byte) (image.Image, error) {
	// The header is read first: a few bytes may claim a picture whose pixels alone would take gigabytes.
	config, _, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("artwork: decode source image: %w", err)
	}
	if int64(config.Width)*int64(config.Height) > maxDecodePixels {
		return nil, fmt.Errorf("artwork: source image is %dx%d, over the %d pixel limit", config.Width, config.Height, maxDecodePixels)
	}
	img, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("artwork: decode source image: %w", err)
	}
	return img, nil
}
