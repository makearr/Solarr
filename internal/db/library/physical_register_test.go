package library_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

var firstSeen = time.Unix(1_700_000_000, 0).UTC()

func TestQuickHash_SameBytesSameHash(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	writeFile(t, fsys, "/media/a.flac", []byte("some flac bytes, more than a token"))
	writeFile(t, fsys, "/media/b.flac", []byte("some flac bytes, more than a token"))

	first, err := library.QuickHash(fsys, "/media/a.flac", 35)
	if err != nil {
		t.Fatalf("QuickHash: %v", err)
	}
	second, err := library.QuickHash(fsys, "/media/b.flac", 35)
	if err != nil {
		t.Fatalf("QuickHash: %v", err)
	}
	if first != second {
		t.Errorf("identical content hashed to %q and %q", first, second)
	}
}

func TestQuickHash_DifferentContentDifferentHash(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	writeFile(t, fsys, "/media/a.flac", []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	writeFile(t, fsys, "/media/b.flac", []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))

	first, err := library.QuickHash(fsys, "/media/a.flac", 35)
	if err != nil {
		t.Fatalf("QuickHash: %v", err)
	}
	second, err := library.QuickHash(fsys, "/media/b.flac", 35)
	if err != nil {
		t.Fatalf("QuickHash: %v", err)
	}
	if first == second {
		t.Error("different content hashed to the same value")
	}
}

// A file larger than twice the sample window exercises the head-and-tail path rather
// than the whole-file one; changing only its middle must not move the hash.
func TestQuickHash_IgnoresTheMiddleOfALargeFile(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	size := 200_000

	first := make([]byte, size)
	second := make([]byte, size)
	copy(first, "head stays the same")
	copy(second, "head stays the same")
	copy(first[size-20:], "tail stays the same")
	copy(second[size-20:], "tail stays the same")
	// Only the middle differs.
	first[size/2] = 'x'
	second[size/2] = 'y'

	writeFile(t, fsys, "/media/a.flac", first)
	writeFile(t, fsys, "/media/b.flac", second)

	hashA, err := library.QuickHash(fsys, "/media/a.flac", int64(size))
	if err != nil {
		t.Fatalf("QuickHash: %v", err)
	}
	hashB, err := library.QuickHash(fsys, "/media/b.flac", int64(size))
	if err != nil {
		t.Fatalf("QuickHash: %v", err)
	}
	if hashA != hashB {
		t.Error("a change only in the middle of a large file moved the quick hash")
	}
}

func writeFile(t *testing.T, fsys *fsx.Mem, path string, content []byte) {
	t.Helper()
	if err := fsys.MkdirAll("/media", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatalf("Create(%q): %v", path, err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("Write(%q): %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", path, err)
	}
}

func TestRegisterBatch_NewFileCreatesObjectAndPath(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)

	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Artist/Album/01.flac", SizeBytes: 1024, QuickHash: "hash-a", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if len(ids) != 1 || ids[0].MediaObjectID == 0 || ids[0].LibraryPathID == 0 {
		t.Fatalf("ids = %+v, want one non-zero media object and library path id", ids)
	}

	object, err := queries.GetMediaObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if object.SizeBytes != 1024 || object.QuickHash != "hash-a" {
		t.Errorf("object = %+v, want size 1024 and quick hash %q", object, "hash-a")
	}

	paths, err := queries.ListPathsForObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if len(paths) != 1 || paths[0].RelPath != "Artist/Album/01.flac" || paths[0].RootFolderID != 1 {
		t.Errorf("paths = %+v, want one row for root 1 at Artist/Album/01.flac", paths)
	}
	if paths[0].ID != ids[0].LibraryPathID {
		t.Errorf("LibraryPathID = %d, want the row RegisterBatch actually created (%d)",
			ids[0].LibraryPathID, paths[0].ID)
	}
}

// A path already registered -- the ordinary case on every sweep after the first --
// must resolve to the same media_object rather than creating a second one.
func TestRegisterBatch_KnownPathReusesTheSameObject(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	file := library.PhysicalFile{
		RelPath: "Artist/Album/01.flac", SizeBytes: 1024, QuickHash: "hash-a", FirstSeenAt: firstSeen,
	}

	first, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil {
		t.Fatalf("RegisterBatch (first): %v", err)
	}
	second, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil {
		t.Fatalf("RegisterBatch (second): %v", err)
	}
	if first[0].MediaObjectID != second[0].MediaObjectID {
		t.Errorf("re-registering the same path returned object %d, then %d",
			first[0].MediaObjectID, second[0].MediaObjectID)
	}
	if first[0].LibraryPathID != second[0].LibraryPathID {
		t.Errorf("re-registering the same path returned library path %d, then %d",
			first[0].LibraryPathID, second[0].LibraryPathID)
	}
}

// Two distinct paths sharing bytes -- a hardlink, most commonly -- reuse the one
// media_object rather than doubling it (ADR-11).
func TestRegisterBatch_SameQuickHashAndSizeDedupesAcrossPaths(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)

	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Artist/Album/01.flac", SizeBytes: 1024, QuickHash: "shared", FirstSeenAt: firstSeen},
		{RelPath: "Artist/Album (Copy)/01.flac", SizeBytes: 1024, QuickHash: "shared", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if ids[0].MediaObjectID != ids[1].MediaObjectID {
		t.Errorf("two paths sharing quick hash and size got objects %d and %d",
			ids[0].MediaObjectID, ids[1].MediaObjectID)
	}
	if ids[0].LibraryPathID == ids[1].LibraryPathID {
		t.Errorf("two distinct paths shared one library_path id (%d) -- a caller that must know "+
			"which path is its own (ImportRevert) needs them to stay distinct", ids[0].LibraryPathID)
	}
}

// The quick hash alone is not identity -- a collision must not merge two files of
// different sizes into one object.
func TestRegisterBatch_SameQuickHashDifferentSizeStaysDistinct(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)

	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "a.flac", SizeBytes: 1024, QuickHash: "collides", FirstSeenAt: firstSeen},
		{RelPath: "b.flac", SizeBytes: 2048, QuickHash: "collides", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if ids[0].MediaObjectID == ids[1].MediaObjectID {
		t.Error("files of different sizes sharing a quick hash were merged into one object")
	}
}
