package unpack_test

import (
	"archive/zip"
	"bytes"
	"io"
	"path"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/importing/unpack"
	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func zipOf(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range members {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func put(t *testing.T, fsys fsx.FS, name string, content []byte) {
	t.Helper()
	if err := fsys.MkdirAll(path.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(file, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func limits() archive.Limits {
	limits := archive.DefaultLimits()
	limits.MinFreeBytes = 0
	return limits
}

// An archive is opened into a folder of the workspace, one inside it too, and the source is left as it was.
func TestExpand_OpensNestedArchivesIntoTheWorkspaceAndLeavesTheSource(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	inner := zipOf(t, map[string][]byte{"03.mp3": []byte("three")})
	outer := zipOf(t, map[string][]byte{"01.mp3": []byte("one"), "02.mp3": []byte("two"), "bonus/extra.zip": inner})
	put(t, fsys, "/dl/Artist/Album.zip", outer)
	put(t, fsys, "/dl/Artist/cover.jpg", []byte("art"))

	opened, err := unpack.Expand(t.Context(), fsys, "/dl", "/cache/ws", limits())
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if opened != 2 {
		t.Errorf("opened %d archives, want the album and the one inside it", opened)
	}
	for _, want := range []string{"/cache/ws/Artist/Album/01.mp3", "/cache/ws/Artist/Album/02.mp3", "/cache/ws/Artist/Album/bonus/extra/03.mp3"} {
		if _, err := fsys.Stat(want); err != nil {
			t.Errorf("%s was not extracted: %v", want, err)
		}
	}
	if _, err := fsys.Stat("/cache/ws/Artist/Album/bonus/extra.zip"); err == nil {
		t.Error("the nested archive is still in the workspace beside the folder it opened to")
	}
	if _, err := fsys.Stat("/dl/Artist/Album.zip"); err != nil {
		t.Errorf("the source archive was touched: %v", err)
	}
}

func TestFind_OneEntryPerMultipartSetAndPerSingleArchive(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, name := range []string{"Album.part01.rar", "Album.part02.rar", "Album.part03.rar", "Other.zip", "notes.txt", "Old.rar", "Old.r00", "Old.r01"} {
		put(t, fsys, "/dl/"+name, []byte("x"))
	}
	found, err := unpack.Find(t.Context(), fsys, "/dl")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Album.part01.rar", "Old.rar", "Other.zip"}
	if !slices.Equal(found, want) {
		t.Errorf("Find = %v, want %v", found, want)
	}
}

func TestRemoveTree_DeletesChildrenFirst(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	put(t, fsys, "/ws/a/b/c.mp3", []byte("x"))
	put(t, fsys, "/ws/d.mp3", []byte("x"))
	if err := unpack.RemoveTree(fsys, "/ws"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Stat("/ws"); err == nil {
		t.Error("the workspace is still there")
	}
}

func TestSweepOlderThan_RemovesOnlyTheOldWorkspaces(t *testing.T) {
	clk := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(clk)
	put(t, fsys, "/ws/old/a.mp3", []byte("x"))
	clk.Advance(48 * time.Hour)
	put(t, fsys, "/ws/new/b.mp3", []byte("x"))

	removed, err := unpack.SweepOlderThan(fsys, "/ws", clk.Now().Add(-24*time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("SweepOlderThan = %d, %v; want the one old workspace", removed, err)
	}
	if _, err := fsys.Stat("/ws/new/b.mp3"); err != nil {
		t.Errorf("the new workspace went too: %v", err)
	}
	if removed, err := unpack.SweepOlderThan(fsys, "/nowhere", clk.Now()); err != nil || removed != 0 {
		t.Errorf("a missing directory = %d, %v; want none", removed, err)
	}
}
