package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestMergeObjectInto_TheDroppedFilesPathsBecomeLinkedPathsOfTheKeptOne(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "A/one.flac", SizeBytes: 10, QuickHash: "aa", FirstSeenAt: firstSeen},
		{RelPath: "B/one.flac", SizeBytes: 12, QuickHash: "bb", FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatal(err)
	}
	keep, drop := registered[0], registered[1]
	if _, err := physical.MergeObjectInto(t.Context(), keep.MediaObjectID, drop.MediaObjectID, map[int64]string{drop.LibraryPathID: "hardlink"}); err != nil {
		t.Fatal(err)
	}
	paths, err := physical.PathsForObject(t.Context(), keep.MediaObjectID)
	if err != nil || len(paths) != 2 {
		t.Fatalf("paths of the kept file = %+v, %v; want both", paths, err)
	}
	for _, path := range paths {
		if path.RelPath == "B/one.flac" && (path.LinkKind != "hardlink" || path.IsPrimary != 0) {
			t.Errorf("merged path = %+v, want a non-primary hardlink", path)
		}
	}
	if exists, _ := physical.Exists(t.Context(), drop.MediaObjectID); exists {
		t.Error("the dropped media object still exists")
	}
}
