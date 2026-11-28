package comic

import (
	"archive/zip"
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func pageImage(t *testing.T, width int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, 4))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestFirstPage_IsTheCoverNamedPageElseTheFirstInReadingOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		pages map[string]int
		order []string
		want  int
	}{
		"numbered pages in natural order": {pages: map[string]int{"10.png": 10, "2.png": 2, "1.png": 1}, order: []string{"10.png", "2.png", "1.png"}, want: 1},
		"a page named cover wins":         {pages: map[string]int{"01.png": 5, "cover.png": 9}, order: []string{"01.png", "cover.png"}, want: 9},
	} {
		fsys := fsx.NewMem(clock.NewFakeAtEpoch())
		if err := fsys.MkdirAll("/lib", 0o755); err != nil {
			t.Fatal(err)
		}
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		for _, page := range tc.order {
			entry, _ := writer.Create("pages/" + page)
			_, _ = entry.Write(pageImage(t, tc.pages[page]))
		}
		_ = writer.Close()
		file, err := fsys.Create("/lib/issue.cbz")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(archive.Bytes())
		_ = file.Close()

		got, err := firstPage(t.Context(), fsys, "/lib/issue.cbz")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		decoded, err := png.DecodeConfig(bytes.NewReader(got))
		if err != nil || decoded.Width != tc.want {
			t.Errorf("%s: page width %d (%v), want %d", name, decoded.Width, err, tc.want)
		}
	}
}
