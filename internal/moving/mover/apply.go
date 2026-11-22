package mover

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// TempPrefix marks a copy still in flight -- never a real destination name, so a crash mid-copy leaves an orphan
// that a later sweep can find and discard, not a same-named partial file mistaken for the real thing.
const TempPrefix = fsx.MoveTempPrefix

// copyBufferBytes is the chunk between context checks, so a 60 GB copy stops within a megabyte of a cancel.
const copyBufferBytes = 1 << 20

// RootPath answers a root folder's own absolute base path -- the caller's
// job (settings.db policy, ADR-25), the same separation music.Consumer's
// own tagSourceFor already keeps from this kind of package.
type RootPath func(rootFolderID int64) (string, bool)

// ErrHashMismatch means a cross-filesystem copy's bytes differ from the source: corruption, not a planning problem; the temporary file is kept for inspection.
var ErrHashMismatch = errors.New("mover: copied file does not match its source")

// ApplyOne performs ADR-51's SAME FS?/COPY/VERIFY steps only; RECORD, UNLINK and TIDY belong to the caller owning the library.db transaction (unlink only after the path is recorded), and the source is left in place.
func ApplyOne(ctx context.Context, fsys fsx.FS, move PlannedMove, roots RootPath) error {
	sourceRoot, ok := roots(move.SourceRootFolderID)
	if !ok {
		return fmt.Errorf("mover: source root %d has no known path", move.SourceRootFolderID)
	}
	destRoot, ok := roots(move.DestRootFolderID)
	if !ok {
		return fmt.Errorf("mover: destination root %d has no known path", move.DestRootFolderID)
	}
	sourceAbs, err := RootRelativePath(fsys, sourceRoot, move.SourceRelPath)
	if err != nil {
		return fmt.Errorf("mover: source path: %w", err)
	}
	destAbs, err := RootRelativePath(fsys, destRoot, move.DestRelPath)
	if err != nil {
		return fmt.Errorf("mover: destination path: %w", err)
	}
	if sourceAbs == destAbs {
		return nil
	}
	if destination, err := fsys.Lstat(destAbs); err == nil {
		if !destination.Mode.IsRegular() {
			return fmt.Errorf("mover: destination %q is not a regular file", destAbs)
		}
		if _, sourceErr := fsys.Lstat(sourceAbs); sourceErr == nil {
			// A prior run may have copied the bytes before it persisted VERIFIED.
			if err := Verify(fsys, sourceAbs, destAbs, move.SizeBytes); err != nil {
				return fmt.Errorf("mover: existing destination %q does not match its source: %w", destAbs, err)
			}
			return nil
		} else if errors.Is(sourceErr, fs.ErrNotExist) && move.SameFilesystem && destination.Size == move.SizeBytes {
			// A same-filesystem rename is atomic: after it succeeds, the source
			// is gone and this sized destination is the completed operation.
			return nil
		} else if !errors.Is(sourceErr, fs.ErrNotExist) {
			return fmt.Errorf("mover: check source %q: %w", sourceAbs, sourceErr)
		}
		return fmt.Errorf("mover: destination %q exists without a verifiable source", destAbs)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mover: check destination %q: %w", destAbs, err)
	}

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

	return copyVerified(ctx, fsys, sourceAbs, destAbs, move.SizeBytes)
}

// RootRelativePath resolves a stored relative path beneath its root, checked where it becomes a filesystem operation:
// a persisted plan's path, or a symlinked parent, may lead outside the library.
func RootRelativePath(fsys fsx.FS, root, relative string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: root %q", fsx.ErrNotAbsolute, root)
	}
	if relative == "" || path.IsAbs(relative) {
		return "", fmt.Errorf("%w: invalid relative path %q", fsx.ErrEscapesRoot, relative)
	}
	resolvedRoot, err := fsys.Resolve(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve root %q: %w", root, err)
	}
	// The parent is resolved, the name is not: a symlinked file is moved or unlinked as the link, never its target.
	candidate := path.Join(resolvedRoot, relative)
	parent, err := fsys.Resolve(path.Dir(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", candidate, err)
	}
	resolved := path.Join(parent, path.Base(candidate))
	if resolved == resolvedRoot || !fsx.Within(resolvedRoot, resolved) {
		return "", fmt.Errorf("%w: %q is not a file beneath %q", fsx.ErrEscapesRoot, resolved, resolvedRoot)
	}
	return resolved, nil
}

// PlaceMethod names the import-ladder rung that placed a file (ADR-11: the reason is recorded), unlike PlannedMove's SameFilesystem which covers moves of files the library already owns.
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

// PlaceOne walks ladder in order and returns the rung that succeeded, because a reflink and a copy look identical on disk; an empty ladder means DefaultLadder, and only PlaceMove removes sourceAbs.
// A failed rung falls through on any error, not just fsx.ErrUnsupported, since both implementations return raw EXDEV across filesystems; when all fail the last error is wrapped in ErrNoRungSucceeded.
func PlaceOne(ctx context.Context, fsys fsx.FS, sourceAbs, destAbs string, sizeBytes int64, ladder []PlaceMethod) (PlaceMethod, error) {
	if len(ladder) == 0 {
		ladder = DefaultLadder
	}
	for _, method := range ladder {
		if !ValidMethod(method) {
			return "", fmt.Errorf("%w: %q", ErrUnknownMethod, method)
		}
	}
	if _, err := fsys.Lstat(destAbs); err == nil {
		return "", fmt.Errorf("mover: destination %q already exists", destAbs)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("mover: check destination %q: %w", destAbs, err)
	}
	if err := fsys.MkdirAll(path.Dir(destAbs), 0o755); err != nil {
		return "", fmt.Errorf("mover: create destination directory for %q: %w", destAbs, err)
	}

	var lastErr error
	for _, method := range ladder {
		err := placeBy(ctx, fsys, method, sourceAbs, destAbs, sizeBytes)
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

// TemporaryPath names the in-flight sibling of destination, on the same filesystem so the final rename is atomic.
func TemporaryPath(destination string) (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("mover: generate temporary name: %w", err)
	}
	return path.Join(path.Dir(destination), TempPrefix+hex.EncodeToString(random)), nil
}

// placeBy attempts exactly one rung, leaving no destination behind on failure
// so the next rung finds the path free rather than already taken.
func placeBy(ctx context.Context, fsys fsx.FS, method PlaceMethod, sourceAbs, destAbs string, sizeBytes int64) error {
	switch method {
	case PlaceReflink:
		return fsys.Reflink(sourceAbs, destAbs)
	case PlaceHardlink:
		return fsys.Link(sourceAbs, destAbs)
	case PlaceSymlink:
		return fsys.Symlink(sourceAbs, destAbs)
	case PlaceMove:
		return moveFile(ctx, fsys, sourceAbs, destAbs, sizeBytes)
	case PlaceCopy:
		return copyVerified(ctx, fsys, sourceAbs, destAbs, sizeBytes)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownMethod, method)
	}
}

// copyVerified copies to an in-flight name, proves the bytes arrived, then renames into place: a kill or a failed
// verify leaves nothing under the final name, and a later rung is not blocked by a partial file.
func copyVerified(ctx context.Context, fsys fsx.FS, sourceAbs, destAbs string, sizeBytes int64) error {
	tempAbs, err := TemporaryPath(destAbs)
	if err != nil {
		return err
	}
	sourceHash, err := copyFile(ctx, fsys, sourceAbs, tempAbs)
	if err != nil {
		_ = fsys.Remove(tempAbs)
		return fmt.Errorf("mover: copy %q to %q: %w", sourceAbs, tempAbs, err)
	}
	if err := verifyAgainst(fsys, tempAbs, sizeBytes, sourceHash); err != nil {
		_ = fsys.Remove(tempAbs)
		return err
	}
	// Both names are under the destination's directory, so this last step is atomic: a reader never sees a partial file.
	if err := fsys.Rename(tempAbs, destAbs); err != nil {
		_ = fsys.Remove(tempAbs)
		return fmt.Errorf("mover: rename %q to %q: %w", tempAbs, destAbs, err)
	}
	return nil
}

// errSourceKept is a move whose verified copy is in place but whose source could not be removed.
var errSourceKept = errors.New("mover: source kept after a verified copy")

// moveFile renames where it can and copies-then-unlinks where it cannot. The source is removed only
// after the copy verifies, so a failed move never destroys the only copy of a file.
func moveFile(ctx context.Context, fsys fsx.FS, sourceAbs, destAbs string, sizeBytes int64) error {
	if err := fsys.Rename(sourceAbs, destAbs); err == nil {
		return nil
	}
	if err := copyVerified(ctx, fsys, sourceAbs, destAbs, sizeBytes); err != nil {
		return err
	}
	if err := fsys.Remove(sourceAbs); err != nil {
		return fmt.Errorf("%w: remove %q: %w", errSourceKept, sourceAbs, err)
	}
	return nil
}

// copyFile copies in chunks, stopping when ctx ends, flushes the destination to disk, and answers the SHA-256 of what it read,
// so verifying needs one read of the source instead of two.
func copyFile(ctx context.Context, fsys fsx.FS, sourceAbs, destAbs string) (string, error) {
	source, err := fsys.Open(sourceAbs)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", sourceAbs, err)
	}
	defer func() { _ = source.Close() }()

	dest, err := fsys.Create(destAbs)
	if err != nil {
		return "", fmt.Errorf("create %q: %w", destAbs, err)
	}
	hash := sha256.New()
	if _, err := io.CopyBuffer(io.MultiWriter(dest, hash), contextReader{ctx: ctx, reader: source}, make([]byte, copyBufferBytes)); err != nil {
		_ = dest.Close()
		return "", fmt.Errorf("write %q: %w", destAbs, err)
	}
	// The source is unlinked next on another filesystem; without a flush a power loss could keep the unlink and lose the copy.
	if syncer, ok := dest.(interface{ Sync() error }); ok {
		if err := syncer.Sync(); err != nil {
			_ = dest.Close()
			return "", fmt.Errorf("flush %q: %w", destAbs, err)
		}
	}
	if err := dest.Close(); err != nil {
		return "", fmt.Errorf("close %q: %w", destAbs, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// contextReader fails a read once its context has ended.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

// verifyAgainst checks a fresh copy's size and its full-content hash against the hash taken while copying.
func verifyAgainst(fsys fsx.FS, destAbs string, expectedSize int64, sourceHash string) error {
	destInfo, err := fsys.Stat(destAbs)
	if err != nil {
		return fmt.Errorf("mover: stat copied file %q: %w", destAbs, err)
	}
	if destInfo.Size != expectedSize {
		return fmt.Errorf("mover: %q copied as %d bytes, want %d: %w", destAbs, destInfo.Size, expectedSize, ErrHashMismatch)
	}
	destHash, err := fullHash(fsys, destAbs)
	if err != nil {
		return fmt.Errorf("mover: hash copied file %q: %w", destAbs, err)
	}
	if destHash != sourceHash {
		return fmt.Errorf("mover: %q does not match its source: %w", destAbs, ErrHashMismatch)
	}
	return nil
}

// Verify checks size then a full content hash, never skipped for a cross-filesystem copy (ADR-51), unlike library.QuickHash's sample, because the point is catching corruption anywhere in the file.
func Verify(fsys fsx.FS, sourceAbs, destAbs string, expectedSize int64) error {
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
