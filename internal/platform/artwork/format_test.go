package artwork_test

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestEncodeAt_EveryFormatDecodesBackToTheSameSize(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			img.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 8), 90, 255})
		}
	}
	for _, format := range artwork.Formats {
		data, err := artwork.EncodeAt(img, format, 80)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		decoded, err := artwork.Decode(data)
		if err != nil {
			t.Fatalf("%s: decode: %v", format, err)
		}
		if got := decoded.Bounds(); got.Dx() != 40 || got.Dy() != 30 {
			t.Errorf("%s decodes to %v, want 40x30", format, got)
		}
	}
}

func TestFindFile_TakesTheFormatInForceThenAnyOtherAndSaysWhenNoneExists(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFake(time.Unix(0, 0)))
	artwork.Configure(artwork.Options{Format: artwork.FormatJPEG})
	t.Cleanup(func() { artwork.Configure(artwork.DefaultOptions) })
	old := artwork.CoverPath("/covers", "work-1-full.webp")
	if err := fsys.MkdirAll("/covers/"+old[len("/covers/"):len(old)-len("work-1-full.webp")-1], 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(old)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if got, found := artwork.FindFile(fsys, "/covers", "work-1-full"); !found || got != old {
		t.Errorf("FindFile with only a webp = %q, %v; want %q found", got, found, old)
	}
	if got, found := artwork.FindFile(fsys, "/covers", "work-2-full"); found || got != artwork.CoverPath("/covers", "work-2-full.jpg") {
		t.Errorf("FindFile of a missing picture = %q, %v; want the jpg path, not found", got, found)
	}
}
