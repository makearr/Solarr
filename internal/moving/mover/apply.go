package mover

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// tempSuffix marks a cross-filesystem copy still in flight -- never a real
// destination name, so a crash mid-copy leaves an orphan this suffix makes
// unambiguous to find and discard on a later sweep, rather than a
// same-named partial file that could be mistaken for the real thing.
const tempSuffix = ".mnx-move-tmp"

// RootPath answers a root folder's own absolute base path -- the caller's
// job (settings.db policy, ADR-25), the same separation music.Consumer's
// own tagSourceFor already keeps from this kind of package.
type RootPath func(rootFolderID int64) (string, bool)

// ErrHashMismatch means a cross-filesystem copy's destination bytes did not
// match its source once written -- corruption during the copy itself, not a
// planning problem. The file at the temporary path is left in place for
// inspection rather than silently discarded.
var ErrHashMismatch = errors.New("mover: copied file does not match its source")

// ApplyOne moves one file exactly as Plan approved it -- ADR-51's own
// Apply algorithm's SAME FS?/COPY/VERIFY steps (2-3). RECORD, UNLINK and
// TIDY are deliberately not this function's job: they touch library.db,
// which this package does not import (the same separation Plan already
// keeps from internal/db/library/internal/db/settings), and "step 5 after step
// 4" (unlink only after library_path is recorded) can only be enforced by
// whichever caller owns that transaction. ApplyOne returns once the bytes
// are safely and verifiably at the destination; the source file is left
// exactly where it was.
func ApplyOne(fsys fsx.FS, move PlannedMove, roots RootPath) error {
	sourceRoot, ok := roots(move.SourceRootFolderID)
	if !ok {
		return fmt.Errorf("mover: source root %d has no known path", move.SourceRootFolderID)
	}
	destRoot, ok := roots(move.DestRootFolderID)
	if !ok {
		return fmt.Errorf("mover: destination root %d has no known path", move.DestRootFolderID)
	}
	sourceAbs := path.Join(sourceRoot, move.SourceRelPath)
	destAbs := path.Join(destRoot, move.DestRelPath)

	if err := fsys.MkdirAll(path.Dir(destAbs), 0o755); err != nil {
		return fmt.Errorf("mover: create destination directory for %q: %w", destAbs, err)
	}

	if move.SameFilesystem {
		// Atomic: either the file is fully at the new path or fully at the
		// old one, never a half state a crash could leave behind.
		if err := fsys.Rename(sourceAbs, destAbs); err != nil {
			return fmt.Errorf("mover: rename %q to %q: %w", sourceAbs, destAbs, err)
		}
		return nil
	}

	tempAbs := destAbs + tempSuffix
	if err := copyFile(fsys, sourceAbs, tempAbs); err != nil {
		return fmt.Errorf("mover: copy %q to %q: %w", sourceAbs, tempAbs, err)
	}
	if err := verify(fsys, sourceAbs, tempAbs, move.SizeBytes); err != nil {
		return err
	}
	// The rename from temp to final name is itself same-filesystem (both
	// under destRoot), so this last step is atomic too -- a reader never
	// sees a partially-written file at the real destination name.
	if err := fsys.Rename(tempAbs, destAbs); err != nil {
		return fmt.Errorf("mover: rename %q to %q: %w", tempAbs, destAbs, err)
	}
	return nil
}

// PlaceMethod names which rung of the import ladder actually placed a file --
// recorded in the import decision (ADR-11: "the reason is recorded"),
// distinct from PlannedMove's SameFilesystem, which is about the four
// already-library-owned move reasons (RENAME/CATEGORY/REBALANCE/DRAIN), not
// import's first placement of a file the library does not own yet.
type PlaceMethod string

// The five rungs a ladder may name. PlaceMove consumes the source and
// PlaceSymlink leaves the library depending on it, so neither is ever
// reached unless the user's own ladder names it (2026-09-18).
const (
	PlaceReflink  PlaceMethod = "reflink"
	PlaceHardlink PlaceMethod = "hardlink"
	PlaceMove     PlaceMethod = "move"
	PlaceCopy     PlaceMethod = "copy"
	PlaceSymlink  PlaceMethod = "symlink"
)

// ErrNoRungSucceeded means every rung the ladder named was refused by the
// filesystem, so the file was not placed at all.
var ErrNoRungSucceeded = errors.New("mover: no transfer method in the ladder could place the file")

// ErrUnknownMethod names a ladder entry that is not one of the five rungs.
var ErrUnknownMethod = errors.New("mover: unknown transfer method")

// DefaultLadder is the order the user gave on 2026-09-18: share the bytes where the filesystem allows
// it, move them where it does not. Symlink only when a user adds it (ADR-11, 2026-09-30).
var DefaultLadder = []PlaceMethod{PlaceReflink, PlaceHardlink, PlaceMove, PlaceCopy}

// ValidMethod reports whether method names a rung PlaceOne can actually walk.
func ValidMethod(method PlaceMethod) bool {
	switch method {
	case PlaceReflink, PlaceHardlink, PlaceMove, PlaceCopy, PlaceSymlink:
		return true
	default:
		return false
	}
}

// ConsumesSource reports whether a rung leaves the source file gone, which is
// what a still-seeding torrent must never be subjected to (private/notes.md).
func ConsumesSource(method PlaceMethod) bool { return method == PlaceMove }

// DependsOnSource reports whether a rung leaves the library pointing at the
// source rather than owning bytes of its own -- Invariant 19's hazard, since
// a source under a cleanup policy takes the library entry with it.
func DependsOnSource(method PlaceMethod) bool { return method == PlaceSymlink }

// PlaceOne places one file at destAbs from sourceAbs by walking ladder in
// order and returns the rung that succeeded, so a caller can report what
// actually happened rather than what was asked for -- a reflink and a copy
// are indistinguishable on disk, which is exactly how an XFS import read as
// "it copied everything" (2026-09-18). An empty ladder means DefaultLadder.
// Only PlaceMove removes sourceAbs, and only when the ladder named it: every
// other rung leaves the source exactly where it was, since an import source
// is not otherwise the library's to delete.
// A failed rung falls through to the next on *any* error, not only
// fsx.ErrUnsupported: fsx.FS.Link's own doc comment promises ErrUnsupported
// across filesystems, but neither the real nor the fake implementation
// actually returns it there (both return a raw EXDEV) -- falling through on
// any error is the contract that is actually honoured. When every rung
// fails the last rung's own error is returned, wrapped in ErrNoRungSucceeded.
func PlaceOne(fsys fsx.FS, sourceAbs, destAbs string, sizeBytes int64, ladder []PlaceMethod) (PlaceMethod, error) {
	if len(ladder) == 0 {
		ladder = DefaultLadder
	}
	for _, method := range ladder {
		if !ValidMethod(method) {
			return "", fmt.Errorf("%w: %q", ErrUnknownMethod, method)
		}
	}
	if err := fsys.MkdirAll(path.Dir(destAbs), 0o755); err != nil {
		return "", fmt.Errorf("mover: create destination directory for %q: %w", destAbs, err)
	}

	var lastErr error
	for _, method := range ladder {
		err := placeBy(fsys, method, sourceAbs, destAbs, sizeBytes)
		if err == nil {
			return method, nil
		}
		if errors.Is(err, errSourceKept) {
			// A read-only download folder: the verified copy is placed, and the source stays as a copy leaves it.
			return PlaceCopy, nil
		}
		lastErr = fmt.Errorf("mover: %s %q to %q: %w", method, sourceAbs, destAbs, err)
	}
	return "", fmt.Errorf("%w: %w", ErrNoRungSucceeded, lastErr)
}

// placeBy attempts exactly one rung, leaving no destination behind on failure
// so the next rung finds the path free rather than already taken.
func placeBy(fsys fsx.FS, method PlaceMethod, sourceAbs, destAbs string, sizeBytes int64) error {
	switch method {
	case PlaceReflink:
		return fsys.Reflink(sourceAbs, destAbs)
	case PlaceHardlink:
		return fsys.Link(sourceAbs, destAbs)
	case PlaceSymlink:
		return fsys.Symlink(sourceAbs, destAbs)
	case PlaceMove:
		return moveFile(fsys, sourceAbs, destAbs, sizeBytes)
	case PlaceCopy:
		return copyVerified(fsys, sourceAbs, destAbs, sizeBytes)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownMethod, method)
	}
}

// copyVerified copies and proves the bytes arrived, discarding a failed
// destination so a later rung is not blocked by a partial file.
func copyVerified(fsys fsx.FS, sourceAbs, destAbs string, sizeBytes int64) error {
	if err := copyFile(fsys, sourceAbs, destAbs); err != nil {
		_ = fsys.Remove(destAbs)
		return err
	}
	if err := verify(fsys, sourceAbs, destAbs, sizeBytes); err != nil {
		_ = fsys.Remove(destAbs)
		return err
	}
	return nil
}

// errSourceKept is a move whose verified copy is in place but whose source could not be removed.
var errSourceKept = errors.New("mover: source kept after a verified copy")

// moveFile renames where it can and copies-then-unlinks where it cannot. The source is removed only
// after the copy verifies, so a failed move never destroys the only copy of a file.
func moveFile(fsys fsx.FS, sourceAbs, destAbs string, sizeBytes int64) error {
	if err := fsys.Rename(sourceAbs, destAbs); err == nil {
		return nil
	}
	if err := copyVerified(fsys, sourceAbs, destAbs, sizeBytes); err != nil {
		return err
	}
	if err := fsys.Remove(sourceAbs); err != nil {
		return fmt.Errorf("%w: remove %q: %w", errSourceKept, sourceAbs, err)
	}
	return nil
}

func copyFile(fsys fsx.FS, sourceAbs, destAbs string) error {
	source, err := fsys.Open(sourceAbs)
	if err != nil {
		return fmt.Errorf("open %q: %w", sourceAbs, err)
	}
	defer func() { _ = source.Close() }()

	dest, err := fsys.Create(destAbs)
	if err != nil {
		return fmt.Errorf("create %q: %w", destAbs, err)
	}
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		return fmt.Errorf("write %q: %w", destAbs, err)
	}
	return dest.Close()
}

// verify checks size then a full content hash, never skipped for a
// cross-filesystem copy (ADR-51's own text: "verified (size plus hash)") --
// unlike library.QuickHash's head-and-tail sample (cheap identity, not
// proof), this reads every byte of both files, since the entire reason for
// a cross-filesystem copy's own verify step is catching corruption
// anywhere in the file, not just its ends.
func verify(fsys fsx.FS, sourceAbs, destAbs string, expectedSize int64) error {
	destInfo, err := fsys.Stat(destAbs)
	if err != nil {
		return fmt.Errorf("mover: stat copied file %q: %w", destAbs, err)
	}
	if destInfo.Size != expectedSize {
		return fmt.Errorf("mover: %q copied as %d bytes, want %d: %w",
			destAbs, destInfo.Size, expectedSize, ErrHashMismatch)
	}

	sourceHash, err := fullHash(fsys, sourceAbs)
	if err != nil {
		return fmt.Errorf("mover: hash source %q: %w", sourceAbs, err)
	}
	destHash, err := fullHash(fsys, destAbs)
	if err != nil {
		return fmt.Errorf("mover: hash copied file %q: %w", destAbs, err)
	}
	if sourceHash != destHash {
		return fmt.Errorf("mover: %q does not match %q: %w", destAbs, sourceAbs, ErrHashMismatch)
	}
	return nil
}

func fullHash(fsys fsx.FS, absolutePath string) (string, error) {
	file, err := fsys.Open(absolutePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
