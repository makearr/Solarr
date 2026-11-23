package comic

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
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

	bin := 0
	writer := ReaderFiles{FS: fsys, KeepOld: func(_ context.Context, _ int64, _ settings.RootFolder, target string) (string, error) {
		bin++
		kept := fmt.Sprintf("/bin/%d.cbz", bin)
		_ = fsys.MkdirAll("/bin", 0o755)
		return kept, fsys.Rename(target, kept)
	}}
	root := settings.RootFolder{ID: 1, Path: "/lib"}
	info := comicinfo.Info{Series: "Lantern", Number: "1", Year: 2031}
	target := "/lib/Lantern/Lantern 001.cbz"
	if counted, err := writer.rewrite(t.Context(), 3, root, target, info, comicinfo.ModeFill, true); err != nil || !counted {
		t.Fatalf("a dry run counts the file that would change: %v, %v", counted, err)
	}
	if before, _ := fsys.Stat(target); before.Size != int64(cbz.Len()) {
		t.Fatalf("a dry run wrote the file: size %d, was %d", before.Size, cbz.Len())
	}
	if changed, err := writer.rewrite(t.Context(), 3, root, target, info, comicinfo.ModeFill, false); err != nil || !changed {
		t.Fatalf("first fill = %v, %v", changed, err)
	}
	if changed, err := writer.rewrite(t.Context(), 3, root, target, info, comicinfo.ModeFill, false); err != nil || changed {
		t.Fatalf("second fill = %v, %v: an unchanged file is not rewritten", changed, err)
	}
	if kept, err := fsys.Stat("/bin/1.cbz"); err != nil || kept.Size != int64(cbz.Len()) || bin != 1 {
		t.Fatalf("the old bytes are in the bin once: %v, %v, %d", kept, err, bin)
	}
	if _, err := fsys.Stat(target + ".solarr-new"); err == nil {
		t.Fatal("the new copy was left beside the file")
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

func TestAFileWhoseOldBytesCannotBeRecycledIsLeftAsItWas(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	_ = fsys.MkdirAll("/lib/Lantern", 0o755)
	var cbz bytes.Buffer
	archive := zip.NewWriter(&cbz)
	page, _ := archive.CreateHeader(&zip.FileHeader{Name: "001.jpg", Method: zip.Store})
	_, _ = page.Write([]byte("page"))
	_ = archive.Close()
	target := "/lib/Lantern/Lantern 001.cbz"
	out, _ := fsys.Create(target)
	_, _ = out.Write(cbz.Bytes())
	_ = out.Close()
	writer := ReaderFiles{FS: fsys, KeepOld: func(context.Context, int64, settings.RootFolder, string) (string, error) {
		return "", errors.New("bin full")
	}}
	changed, err := writer.rewrite(t.Context(), 3, settings.RootFolder{ID: 1, Path: "/lib"}, target, comicinfo.Info{Series: "Lantern"}, comicinfo.ModeFill, false)
	if err == nil || changed {
		t.Fatalf("rewrite = %v, %v", changed, err)
	}
	if kept, statErr := fsys.Stat(target); statErr != nil || kept.Size != int64(cbz.Len()) {
		t.Fatalf("the file changed: %v, %v", kept, statErr)
	}
	if _, statErr := fsys.Stat(target + ".solarr-new"); statErr == nil {
		t.Fatal("the new copy was left beside the file")
	}
}

func TestAMangaVolumeIsDescribedByItsVolumeAndReadsRightToLeft(t *testing.T) {
	detail := library.SeriesDetailRow{Summary: library.SeriesSummary{Title: "Night Harbor", Year: 2019, Fields: map[string]string{"publisher": "Acme Press"}}}
	info := issueInfo(detail, library.PartPath{MediaRef: "volume:3", Date: "2020-05-06"}, true)
	if info.Volume != 3 || info.Number != "" || info.Manga != "YesAndRightToLeft" || info.Year != 2020 || info.Month != 5 || info.Day != 6 || info.Publisher != "Acme Press" {
		t.Fatalf("manga info = %+v", info)
	}
	if issue := issueInfo(detail, library.PartPath{MediaRef: "issue:7"}, false); issue.Number != "7" || issue.Volume != 2019 || issue.Manga != "" {
		t.Fatalf("issue info = %+v", issue)
	}
}
