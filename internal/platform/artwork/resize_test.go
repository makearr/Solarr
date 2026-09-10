package artwork_test

import (
	"image"
	"testing"

	"github.com/makearr/MediaNexus/internal/platform/artwork"
)

func TestResizeLongestEdge_ScalesTheLongerSideToTheTarget(t *testing.T) {
	tests := []struct {
		name                  string
		width, height         int
		edge                  int
		wantWidth, wantHeight int
	}{
		{"landscape scales down", 2000, 1000, 400, 400, 200},
		{"portrait scales down", 1000, 2000, 400, 200, 400},
		{"square scales down", 500, 500, 160, 160, 160},
		{"source smaller than target is never upscaled", 100, 80, 1000, 100, 80},
		{"edge equal to the source's longest side is a no-op", 300, 150, 300, 300, 150},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := image.NewRGBA(image.Rect(0, 0, tt.width, tt.height))
			resized := artwork.ResizeLongestEdge(source, tt.edge)
			bounds := resized.Bounds()
			if bounds.Dx() != tt.wantWidth || bounds.Dy() != tt.wantHeight {
				t.Errorf("ResizeLongestEdge(%dx%d, %d) = %dx%d, want %dx%d",
					tt.width, tt.height, tt.edge, bounds.Dx(), bounds.Dy(), tt.wantWidth, tt.wantHeight)
			}
		})
	}
}
