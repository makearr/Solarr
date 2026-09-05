package mover

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/makearr/MediaNexus/internal/fsx"
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
// keeps from internal/library/internal/settings), and "step 5 after step
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
