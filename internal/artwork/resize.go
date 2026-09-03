package artwork

import (
	"image"

	"golang.org/x/image/draw"
)

// ResizeLongestEdge scales img so its longer side is edge pixels, preserving
// aspect ratio. It never upscales -- a size larger than the source only asks
// for pixels that do not exist, so the source's own resolution is the ceiling.
func ResizeLongestEdge(img image.Image, edge int) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if edge >= width && edge >= height {
		edge = max(width, height)
	}

	var targetWidth, targetHeight int
	if width >= height {
		targetWidth = edge
		targetHeight = max(1, height*edge/width)
	} else {
		targetHeight = edge
		targetWidth = max(1, width*edge/height)
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Src, nil)
	return dst
}
