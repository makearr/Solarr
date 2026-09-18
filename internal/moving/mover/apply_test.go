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

	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), nil)
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

	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), nil)
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
// PlaceOne falls all the way to a verified copy -- the ladder is named
// explicitly because the shipped default reaches move first (2026-09-18).
// this pass implements ("move" is deliberately not built, questions.md).
func TestPlaceOne_FallsBackToCopyAcrossFilesystems(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetReflinkSupported(false)
	fsys.SetHardlinkSupported(false)
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceReflink, mover.PlaceHardlink, mover.PlaceCopy}
	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), ladder)
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

	ladder := []mover.PlaceMethod{mover.PlaceReflink, mover.PlaceHardlink, mover.PlaceCopy}
	_, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes"))+1, ladder)
	if err == nil || !errors.Is(err, mover.ErrHashMismatch) {
		t.Fatalf("PlaceOne = %v, want ErrHashMismatch for the wrong expected size", err)
	}
}

// The ladder is walked in the caller's own order, not a fixed one: asking for
// hardlink first on a filesystem that also supports reflink must hardlink.
func TestPlaceOne_WalksTheLadderInOrder(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceHardlink, mover.PlaceReflink, mover.PlaceCopy}
	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), ladder)
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceHardlink {
		t.Errorf("method = %q, want hardlink -- the ladder's own first entry", method)
	}
}

// Move is the one rung that consumes the source, and only when named.
func TestPlaceOne_MoveConsumesTheSource(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceMove}
	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), ladder)
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceMove {
		t.Errorf("method = %q, want move", method)
	}
	if readFile(t, fsys, "/library/New/track.flac") != "the bytes" {
		t.Error("destination does not have the source's content")
	}
	if exists(fsys, "/downloads/Old/track.flac") {
		t.Error("move must remove the source once the destination is in place")
	}
}

// A cross-filesystem move copies, verifies, and only then unlinks -- a failed
// move must never be the reason the only copy of a file disappeared.
func TestPlaceOne_MoveAcrossFilesystemsVerifiesBeforeUnlinking(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetHardlinkSupported(false)
	fsys.SetReflinkSupported(false)
	fsys.SetRenameSupported(false)
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceMove}
	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), ladder)
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceMove {
		t.Errorf("method = %q, want move", method)
	}
	if readFile(t, fsys, "/library/New/track.flac") != "the bytes" {
		t.Error("destination does not have the source's content")
	}
	if exists(fsys, "/downloads/Old/track.flac") {
		t.Error("move must remove the source once the copy verified")
	}
}

// A move whose copy fails verification leaves the source alone: losing the
// destination is recoverable, losing the source is not.
func TestPlaceOne_MoveKeepsTheSourceWhenVerifyFails(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetHardlinkSupported(false)
	fsys.SetReflinkSupported(false)
	fsys.SetRenameSupported(false)
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceMove}
	_, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes"))+1, ladder)
	if err == nil {
		t.Fatal("PlaceOne = nil, want a failure for the wrong expected size")
	}
	if !exists(fsys, "/downloads/Old/track.flac") {
		t.Error("a failed move must never remove the source")
	}
}

// Symlink is reachable only because the user's own ladder named it -- ADR-11
// bans it as a default, not as a choice (2026-09-18).
func TestPlaceOne_SymlinkOnlyWhenTheLadderNamesIt(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceSymlink}
	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), ladder)
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceSymlink {
		t.Errorf("method = %q, want symlink", method)
	}
	if !exists(fsys, "/downloads/Old/track.flac") {
		t.Error("symlink must never remove the source")
	}
}

// A ladder naming only a method the filesystem refuses fails loudly rather
// than falling back to something the user did not ask for.
func TestPlaceOne_FailsWhenNoRungSucceeds(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetHardlinkSupported(false)
	fsys.SetReflinkSupported(false)
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceReflink, mover.PlaceHardlink}
	_, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), ladder)
	if err == nil || !errors.Is(err, mover.ErrNoRungSucceeded) {
		t.Fatalf("PlaceOne = %v, want ErrNoRungSucceeded", err)
	}
	if exists(fsys, "/library/New/track.flac") {
		t.Error("nothing may be left at the destination when every rung failed")
	}
}

// An unknown method is refused before anything is placed, so a typo in the
// setting cannot half-import a unit.
func TestPlaceOne_RefusesAnUnknownMethodBeforePlacing(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	ladder := []mover.PlaceMethod{mover.PlaceHardlink, mover.PlaceMethod("teleport")}
	_, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), ladder)
	if err == nil || !errors.Is(err, mover.ErrUnknownMethod) {
		t.Fatalf("PlaceOne = %v, want ErrUnknownMethod", err)
	}
	if exists(fsys, "/library/New/track.flac") {
		t.Error("an invalid ladder must be refused before any rung runs")
	}
}

// A rung that fails must leave nothing at the destination, or the next rung
// finds the path already taken and a corrupt file sits at a real library name.
func TestPlaceOne_AFailedCopyRungLeavesTheDestinationFreeForTheNextOne(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/downloads/Old/track.flac", "the bytes")

	// The size is a lie, so the copy rung's verify refuses bytes that are in
	// fact correct -- the hardlink rung behind it must still be reachable.
	ladder := []mover.PlaceMethod{mover.PlaceCopy, mover.PlaceHardlink}
	method, err := mover.PlaceOne(fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes"))+1, ladder)
	if err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if method != mover.PlaceHardlink {
		t.Errorf("method = %q, want hardlink -- the failed copy must not block the next rung", method)
	}
}
