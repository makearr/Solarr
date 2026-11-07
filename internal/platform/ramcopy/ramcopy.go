// Package ramcopy keeps a folder's working copy in a RAM-backed directory and copies it back to disk.
package ramcopy

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Headroom is the share added to a folder's size when sizing the RAM it needs.
const Headroom = 1.10

// WithHeadroom is bytes plus the headroom, rounded up.
func WithHeadroom(bytes int64) int64 { return int64(float64(bytes)*Headroom) + 1 }

// DirBytes adds up the files under a folder; one that does not exist holds nothing.
func DirBytes(ctx context.Context, fsys fsx.FS, folder string) (int64, error) {
	entries, err := fsys.ReadDir(folder)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		child := filepath.Join(folder, entry.Name)
		if entry.IsDir {
			bytes, err := DirBytes(ctx, fsys, child)
			if err != nil {
				return total, err
			}
			total += bytes
			continue
		}
		if info, err := fsys.Stat(child); err == nil {
			total += info.Size
		}
	}
	return total, nil
}

// CopyFile copies one file atomically, keeping its modification time so a later Sync can tell it unchanged.
func CopyFile(fsys fsx.FS, from, to string) error {
	info, err := fsys.Stat(from)
	if err != nil {
		return err
	}
	source, err := fsys.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	if err := fsys.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := fsx.WriteAtomicSecret(fsys, to, func(w io.Writer) error { _, err := io.Copy(w, source); return err }); err != nil {
		return err
	}
	return fsys.Chtimes(to, info.ModTime)
}

// Sync makes the folder to hold what from holds: new and changed files are copied, files only in to are removed.
// It answers how many files it copied and removed.
func Sync(ctx context.Context, fsys fsx.FS, from, to string) (copied, removed int, err error) {
	if err := fsys.MkdirAll(to, 0o755); err != nil {
		return 0, 0, err
	}
	source, err := fsys.ReadDir(from)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	present := map[string]bool{}
	for _, entry := range source {
		if err := ctx.Err(); err != nil {
			return copied, removed, err
		}
		present[entry.Name] = true
		fromPath, toPath := filepath.Join(from, entry.Name), filepath.Join(to, entry.Name)
		if entry.IsDir {
			c, r, err := Sync(ctx, fsys, fromPath, toPath)
			copied, removed = copied+c, removed+r
			if err != nil {
				return copied, removed, err
			}
			continue
		}
		sourceInfo, err := fsys.Stat(fromPath)
		if err != nil {
			continue
		}
		if targetInfo, err := fsys.Stat(toPath); err == nil && targetInfo.Size == sourceInfo.Size && targetInfo.ModTime.Equal(sourceInfo.ModTime) {
			continue
		}
		if err := CopyFile(fsys, fromPath, toPath); err != nil {
			return copied, removed, err
		}
		copied++
	}
	target, err := fsys.ReadDir(to)
	if err != nil {
		return copied, removed, err
	}
	for _, entry := range target {
		if present[entry.Name] || entry.IsDir {
			continue
		}
		if err := fsys.Remove(filepath.Join(to, entry.Name)); err != nil {
			return copied, removed, err
		}
		removed++
	}
	return copied, removed, nil
}
