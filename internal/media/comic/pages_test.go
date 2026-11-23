package comic

import (
	"archive/zip"
	"bytes"
	"io"
	"path"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func writePageFile(t *testing.T, fsys *fsx.Mem, absolute string) {
	t.Helper()
	if err := fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(absolute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("bytes of " + path.Base(absolute))); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
}

// zipEntry is one member of a packed file, as a reader sees it.
type zipEntry struct {
	Name   string
	Method uint16
	Body   string
}

func readPacked(t *testing.T, fsys *fsx.Mem, absolute string) []zipEntry {
	t.Helper()
	file, err := fsys.Open(absolute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var entries []zipEntry
	for _, member := range reader.File {
		body, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(body)
		_ = body.Close()
		entries = append(entries, zipEntry{Name: member.Name, Method: member.Method, Body: string(content)})
	}
	return entries
}

// A download holding an issue as loose pages becomes one store-only cbz named after its folder, pages in reading order
// (a cover first, "2" before "10"); art, forks, too-few-page folders and a folder of archives are left out, and the
// source is only read.
func TestPackPageFolders_PacksEachFolderOfPagesIntoOneCBZ(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	source := "/dl/Invented Hero 001 (2031)"
	for _, name := range []string{"10.jpg", "2.jpg", "1.PNG", "cover.jpg", "folder.jpg", "._3.jpg", "notes.txt"} {
		writePageFile(t, fsys, path.Join(source, name))
	}
	for _, name := range []string{"Bonus/a.webp", "Bonus/b.webp", "Bonus/c.webp", "Art/back.jpg", "Art/front.jpg",
		"Held/Invented Hero 002.cbz", "Held/p1.jpg", "Held/p2.jpg", "Held/p3.jpg"} {
		writePageFile(t, fsys, path.Join(source, name))
	}
	if err := fsys.Symlink(path.Join(source, "2.jpg"), path.Join(source, "Bonus/d.webp")); err != nil {
		t.Fatal(err)
	}

	packed, err := PackPageFolders(t.Context(), fsys, source, "/ws")
	if err != nil {
		t.Fatalf("PackPageFolders: %v", err)
	}
	want := []batch.PackedFolder{
		{File: "/ws/Invented Hero 001 (2031).cbz", Pages: []string{source + "/cover.jpg", source + "/1.PNG", source + "/2.jpg", source + "/10.jpg"}},
		{File: "/ws/Bonus.cbz", Pages: []string{source + "/Bonus/a.webp", source + "/Bonus/b.webp", source + "/Bonus/c.webp"}},
	}
	if !reflect.DeepEqual(packed, want) {
		t.Fatalf("packed = %+v, want %+v", packed, want)
	}
	wantEntries := []zipEntry{
		{Name: "cover.jpg", Method: zip.Store, Body: "bytes of cover.jpg"},
		{Name: "1.PNG", Method: zip.Store, Body: "bytes of 1.PNG"},
		{Name: "2.jpg", Method: zip.Store, Body: "bytes of 2.jpg"},
		{Name: "10.jpg", Method: zip.Store, Body: "bytes of 10.jpg"},
	}
	if got := readPacked(t, fsys, want[0].File); !reflect.DeepEqual(got, wantEntries) {
		t.Errorf("entries = %+v, want %+v", got, wantEntries)
	}
	entries, err := fsys.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	slices.Sort(names)
	if wantNames := []string{"._3.jpg", "1.PNG", "10.jpg", "2.jpg", "Art", "Bonus", "Held", "cover.jpg", "folder.jpg", "notes.txt"}; !slices.Equal(names, wantNames) {
		t.Errorf("source = %v, want it untouched: %v", names, wantNames)
	}
}

// One issue's pages weigh at most what a single issue may: a larger folder is refused before anything is written.
func TestWritePages_RefusesMoreThanOneIssueWeighs(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writePageFile(t, fsys, "/dl/a.jpg")
	writePageFile(t, fsys, "/dl/b.jpg")
	pages := []page{{path: "/dl/a.jpg", info: fsx.FileInfo{Size: maxPackedBytes}}, {path: "/dl/b.jpg", info: fsx.FileInfo{Size: 1}}}
	if err := writePages(t.Context(), fsys, "/ws/Huge.cbz", pages); err == nil {
		t.Fatal("writePages accepted more than one issue weighs")
	}
	if _, err := fsys.Stat("/ws/Huge.cbz"); err == nil {
		t.Error("a refused folder left a file")
	}
}

// The walk stops maxPackDepth folders down, and a packed file already in the workspace is never written over.
func TestPackPageFolders_StopsAtTheDepthLimitAndKeepsAnExistingFile(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	deep := "/dl/Invented"
	for range maxPackDepth + 1 {
		deep += "/d"
	}
	for _, name := range []string{"1.jpg", "2.jpg", "3.jpg"} {
		writePageFile(t, fsys, path.Join(deep, name))
		writePageFile(t, fsys, path.Join("/dl/Invented", name))
	}
	writePageFile(t, fsys, "/ws/Invented.cbz")

	packed, err := PackPageFolders(t.Context(), fsys, "/dl/Invented", "/ws")
	if err == nil || packed != nil {
		t.Errorf("packed = %+v, %v; want nothing and the existing file refused", packed, err)
	}
	if got := readFileText(t, fsys, "/ws/Invented.cbz"); got != "bytes of Invented.cbz" {
		t.Errorf("existing file = %q, overwritten", got)
	}
}

func readFileText(t *testing.T, fsys *fsx.Mem, absolute string) string {
	t.Helper()
	file, err := fsys.Open(absolute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	return string(data)
}
