package comic

import (
	"archive/zip"
	"bytes"
	"errors"
	"image/png"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func issueOf(t *testing.T, entries map[string]int) (*PageReader, string) {
	t.Helper()
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/lib", 0o755); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"10.png", "2.png", "cover.png", "1.png", "notes.txt", "folder.png"} {
		width, found := entries[name]
		if !found {
			continue
		}
		entry, _ := writer.Create("issue/" + name)
		if name == "notes.txt" {
			_, _ = entry.Write([]byte("text"))
			continue
		}
		_, _ = entry.Write(pageImage(t, width))
	}
	_ = writer.Close()
	file, err := fsys.Create("/lib/issue.cbz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(archive.Bytes())
	_ = file.Close()
	return &PageReader{FS: fsys}, "/lib/issue.cbz"
}

func TestPageReader_ListsOnlyPagesInReadingOrderAndReadsOne(t *testing.T) {
	reader, issue := issueOf(t, map[string]int{"10.png": 10, "2.png": 2, "cover.png": 9, "1.png": 1, "notes.txt": 0, "folder.png": 7})
	names, err := reader.PageNames(t.Context(), issue)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"issue/cover.png", "issue/1.png", "issue/2.png", "issue/10.png"}; !slices.Equal(names, want) {
		t.Errorf("pages = %v, want %v", names, want)
	}
	data, contentType, err := reader.ReadPage(t.Context(), issue, "issue/2.png")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || decoded.Width != 2 || contentType != "image/png" {
		t.Errorf("page 2 = width %d (%v) as %q, want width 2 as image/png", decoded.Width, err, contentType)
	}
	if _, _, err := reader.ReadPage(t.Context(), issue, "issue/notes.txt"); !errors.Is(err, ErrNoSuchPage) {
		t.Errorf("reading a non-page = %v, want ErrNoSuchPage", err)
	}
}
