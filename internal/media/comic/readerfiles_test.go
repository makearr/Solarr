package comic

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/comicinfo"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestARepeatedFillRewritesTheCbzOnlyOnce(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/lib/Lantern", 0o755); err != nil {
		t.Fatal(err)
	}
	var cbz bytes.Buffer
	archive := zip.NewWriter(&cbz)
	page, _ := archive.CreateHeader(&zip.FileHeader{Name: "001.jpg", Method: zip.Store})
	_, _ = page.Write([]byte("page"))
	_ = archive.Close()
	out, err := fsys.Create("/lib/Lantern/Lantern 001.cbz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = out.Write(cbz.Bytes())
	_ = out.Close()

	writer := ReaderFiles{FS: fsys}
	info := comicinfo.Info{Series: "Lantern", Number: "1", Year: 2031}
	target := "/lib/Lantern/Lantern 001.cbz"
	file := library.PartPath{MediaRef: "issue:1"}
	if changed, err := writer.rewrite(t.Context(), target, file, info, comicinfo.ModeFill); err != nil || !changed {
		t.Fatalf("first fill = %v, %v", changed, err)
	}
	if changed, err := writer.rewrite(t.Context(), target, file, info, comicinfo.ModeFill); err != nil || changed {
		t.Fatalf("second fill = %v, %v: an unchanged file is not rewritten", changed, err)
	}
	opened, _ := fsys.Open(target)
	defer func() { _ = opened.Close() }()
	stat, _ := fsys.Stat(target)
	data, err := comicinfo.ReadFromCBZ(opened, stat.Size)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := comicinfo.ParseComicInfo(data); got.Series != "Lantern" || got.Year != 2031 {
		t.Fatalf("written info = %+v", got)
	}
	reader, _ := zip.NewReader(opened, stat.Size)
	body, _ := reader.File[0].Open()
	if text, _ := io.ReadAll(body); string(text) != "page" {
		t.Fatalf("page = %q", text)
	}
}
