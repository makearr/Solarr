package artwork

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder with image.Decode
	_ "image/png"  // registers the PNG decoder with image.Decode

	_ "golang.org/x/image/webp" // registers the WebP decoder: the cache's own covers are WebP
)

// Decode reads whatever a provider actually sends -- Cover Art Archive serves
// JPEG and PNG in practice, so both decoders are registered above.
func Decode(source []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("artwork: decode source image: %w", err)
	}
	return img, nil
}
