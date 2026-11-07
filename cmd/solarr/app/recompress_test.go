package app

import (
	"image"
	"image/color"
	"io"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func noisy(width, height int) *image.RGBA {
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 13), uint8(x ^ y), 255})
		}
	}
	return picture
}

func TestRecompress_ShrinksCoversAndPreviewsToTheNewFiguresAndNeverGrowsOne(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	cover, err := artwork.EncodeWebP(noisy(1000, 1000))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := artwork.EncodeJPEGAt(noisy(1280, 720), 95)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, fsys, "/mediacover/work-1-cover-full.webp", string(cover))
	putFile(t, fsys, "/previews/0/5-frame.jpg", string(preview))

	run := &imageRecompress{
		fs: fsys, coverDir: "/mediacover", previewsDir: "/previews", logger: slog.New(slog.DiscardHandler),
		images: func() settings.ImageSettings {
			return settings.ImageSettings{CoverMaxEdge: 500, CoverQuality: 60, PreviewMaxEdge: 640, PreviewQuality: 50}
		},
	}
	run.run(t.Context())

	status := run.Status()
	if status.Total != 2 || status.Done != 2 || status.Failed != 0 || status.SavedBytes <= 0 {
		t.Fatalf("status = %+v; want 2 done, none failed, bytes saved", status)
	}
	sizeOf := func(path string) (int, int) {
		file, err := fsys.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(file)
		_ = file.Close()
		img, err := artwork.Decode(data)
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return img.Bounds().Dx(), img.Bounds().Dy()
	}
	if width, _ := sizeOf("/mediacover/work-1-cover-full.webp"); width != 500 {
		t.Errorf("cover width = %d, want 500", width)
	}
	if width, _ := sizeOf("/previews/0/5-frame.jpg"); width != 640 {
		t.Errorf("preview width = %d, want 640", width)
	}

	// Run again with the same figures: nothing comes out smaller, so nothing is rewritten.
	before := run.Status().SavedBytes
	run.status.Running = false
	run.run(t.Context())
	if got := run.Status().SavedBytes; got != before {
		t.Errorf("a second run saved %d more bytes; want it to leave settled pictures alone", got-before)
	}
}
