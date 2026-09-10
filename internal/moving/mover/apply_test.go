package mover_test

import (
	"errors"
	"io"
	"testing"

	"github.com/makearr/MediaNexus/internal/moving/mover"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

func writeFile(t *testing.T, fsys *fsx.Mem, absolutePath, content string) {
	t.Helper()
	if err := fsys.MkdirAll(dirOf(absolutePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	f, err := fsys.Create(absolutePath)
	if err != nil {
		t.Fatalf("Create(%q): %v", absolutePath, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func dirOf(absolutePath string) string {
	i := len(absolutePath) - 1
	for i >= 0 && absolutePath[i] != '/' {
		i--
	}
	return absolutePath[:i]
}

func readFile(t *testing.T, fsys *fsx.Mem, absolutePath string) string {
	t.Helper()
	f, err := fsys.Open(absolutePath)
	if err != nil {
		t.Fatalf("Open(%q): %v", absolutePath, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(data)
}

func exists(fsys *fsx.Mem, absolutePath string) bool {
	_, err := fsys.Stat(absolutePath)
	return err == nil
}

func rootPathOf(roots map[int64]string) mover.RootPath {
	return func(id int64) (string, bool) {
		p, ok := roots[id]
		return p, ok
	}
}

// A same-filesystem move is a real rename: the destination gets the
// content, and the source is gone, since rename(2) *is* the move -- there
// is nothing left for a caller to unlink afterward.
func TestApplyOne_SameFilesystemRenames(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/root1/Old/track.flac", "the bytes")
	roots := rootPathOf(map[int64]string{1: "/root1", 2: "/root2"})

	move := mover.PlannedMove{
		SourceRootFolderID: 1, SourceRelPath: "Old/track.flac",
		DestRootFolderID: 2, DestRelPath: "New/track.flac",
		SizeBytes: int64(len("the bytes")), SameFilesystem: true,
	}
	if err := mover.ApplyOne(fsys, move, roots); err != nil {
		t.Fatalf("ApplyOne: %v", err)
	}
	if readFile(t, fsys, "/root2/New/track.flac") != "the bytes" {
		t.Error("destination does not have the source's content")
	}
	if exists(fsys, "/root1/Old/track.flac") {
		t.Error("source still exists after a same-filesystem rename")
	}
}

// A cross-filesystem move copies and verifies, but leaves the source in
// place -- ADR-51's own ordering (RECORD, then UNLINK) means only the
// caller removes it, once library_path is safely repointed.
func TestApplyOne_CrossFilesystemCopiesButLeavesTheSource(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/root1/Old/track.flac", "the bytes")
	roots := rootPathOf(map[int64]string{1: "/root1", 2: "/root2"})

	move := mover.PlannedMove{
		SourceRootFolderID: 1, SourceRelPath: "Old/track.flac",
		DestRootFolderID: 2, DestRelPath: "New/track.flac",
		SizeBytes: int64(len("the bytes")), SameFilesystem: false,
	}
	if err := mover.ApplyOne(fsys, move, roots); err != nil {
		t.Fatalf("ApplyOne: %v", err)
	}
	if readFile(t, fsys, "/root2/New/track.flac") != "the bytes" {
		t.Error("destination does not have the source's content")
	}
	if !exists(fsys, "/root1/Old/track.flac") {
		t.Error("source was removed by ApplyOne -- unlink is the caller's job, after RECORD")
	}
	if exists(fsys, "/root2/New/track.flac"+".mnx-move-tmp") {
		t.Error("a temporary copy was left behind instead of being renamed into place")
	}
}

func TestApplyOne_RefusesAnUnknownSourceRoot(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	roots := rootPathOf(map[int64]string{2: "/root2"})
	move := mover.PlannedMove{SourceRootFolderID: 1, DestRootFolderID: 2}
	if err := mover.ApplyOne(fsys, move, roots); err == nil {
		t.Fatal("ApplyOne succeeded despite an unresolvable source root")
	}
}

// A cross-filesystem copy is verified against the size Plan itself
// recorded, not just against whatever landed at the destination -- if the
// source changed size between Plan and Apply (a real race, not just
// corruption during the copy), that must be caught too.
func TestApplyOne_DetectsASizeMismatchAfterCopying(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/root1/Old/track.flac", "the bytes")
	roots := rootPathOf(map[int64]string{1: "/root1", 2: "/root2"})

	move := mover.PlannedMove{
		SourceRootFolderID: 1, SourceRelPath: "Old/track.flac",
		DestRootFolderID: 2, DestRelPath: "New/track.flac",
		SizeBytes:      int64(len("the bytes")) + 1, // claims one more byte than the source really has
		SameFilesystem: false,
	}
	err := mover.ApplyOne(fsys, move, roots)
	if err == nil || !errors.Is(err, mover.ErrHashMismatch) {
		t.Fatalf("ApplyOne = %v, want ErrHashMismatch for the wrong expected size", err)
	}
}

// PlaceOne prefers reflink first (ADR-11 A's own ladder), on a filesystem
// that supports it, and the source is left exactly where it was -- unlike
// ApplyOne's same-filesystem Rename, nothing about import placement may ever
// touch the source (a download client's own file, not the library's yet).
func TestPlaceOne_PrefersReflinkAndLeavesTheSource(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")))
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceReflink {
		t.Errorf("method = %q, want reflink", method)
	}
	if readFile(t, fsys, "/library/New/track.flac") != "the bytes" {
		t.Error("destination does not have the source's content")
	}
	if !exists(fsys, "/downloads/Old/track.flac") {
		t.Error("PlaceOne must never remove the source")
	}
}

// Without reflink support, the next rung is hardlink -- still same-content,
// still leaves the source untouched (a hardlink is a second directory entry
// to the same bytes, not a move).
func TestPlaceOne_FallsBackToHardlinkWithoutReflink(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetReflinkSupported(false)
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")))
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceHardlink {
		t.Errorf("method = %q, want hardlink", method)
	}
	if readFile(t, fsys, "/library/New/track.flac") != "the bytes" {
		t.Error("destination does not have the source's content")
	}
	if !exists(fsys, "/downloads/Old/track.flac") {
		t.Error("PlaceOne must never remove the source")
	}
}

// With neither reflink nor hardlink available (the cross-filesystem case),
// PlaceOne falls all the way to a verified copy -- ADR-11 A's own last rung
// this pass implements ("move" is deliberately not built, questions.md).
func TestPlaceOne_FallsBackToCopyAcrossFilesystems(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetReflinkSupported(false)
	fsys.SetHardlinkSupported(false)
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")))
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceCopy {
		t.Errorf("method = %q, want copy", method)
	}
	if readFile(t, fsys, "/library/New/track.flac") != "the bytes" {
		t.Error("destination does not have the source's content")
	}
	if !exists(fsys, "/downloads/Old/track.flac") {
		t.Error("PlaceOne must never remove the source")
	}
}

// A copy that lands the wrong size is caught the same way ApplyOne's own
// cross-filesystem verify catches it -- PlaceOne's copy rung reuses the
// exact same verify step, not a second, unverified copy path.
func TestPlaceOne_DetectsASizeMismatchOnCopy(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetReflinkSupported(false)
	fsys.SetHardlinkSupported(false)
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	_, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes"))+1)
	if err == nil || !errors.Is(err, mover.ErrHashMismatch) {
		t.Fatalf("PlaceOne = %v, want ErrHashMismatch for the wrong expected size", err)
	}
}
