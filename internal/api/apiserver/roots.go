package apiserver

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/privilege"
)

// FilesystemRoots checks a candidate library root against a real filesystem.
// See documentation/container-runtime.md.
type FilesystemRoots struct {
	FS fsx.FS
}

// Validate refuses a path that cannot be a library root at all.
func (r FilesystemRoots) Validate(path string) error {
	if path == "" {
		return errors.New("a path is required")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path", path)
	}

	info, err := r.FS.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%q does not exist inside the container; "+
				"check the volume is mounted at that path", path)
		}
		return fmt.Errorf("%q cannot be read: %w", path, err)
	}
	if !info.IsDir {
		return fmt.Errorf("%q is a file, not a folder", path)
	}
	return nil
}

// Writable reports whether this process can write here, naming the owner if
// not. A library root is never chowned.
func (r FilesystemRoots) Writable(path string) error {
	if err := r.probeWritable(path); err != nil {
		info, statErr := r.FS.Stat(path)
		if statErr != nil {
			return err
		}
		return fmt.Errorf("%q is owned by uid=%d gid=%d with mode %04o; %s. "+
			"Either chown it to match, or set PUID and PGID to whoever owns it",
			path, info.UID, info.GID, info.Mode.Perm(), privilege.IdentityHint())
	}
	return nil
}

func (r FilesystemRoots) probeWritable(directory string) error {
	probe := filepath.Join(directory, ".mnx-write-probe")

	file, err := r.FS.Create(probe)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Or every root the user adds gets a stray file.
	return r.FS.Remove(probe)
}

// Readable reports whether this process can list a download folder's
// contents. Read-only, unlike Writable's create-then-remove probe: a
// download folder is never written to by Solarr (section 5.1) -- it is
// someone else's client writing there, only ever watched from here.
func (r FilesystemRoots) Readable(path string) error {
	_, err := r.FS.ReadDir(path)
	return err
}

// Usage reports the filesystem's total and free bytes, the same numbers the capacity sweep records.
func (r FilesystemRoots) Usage(path string) (totalBytes, freeBytes int64, err error) {
	return r.FS.DiskUsage(path)
}
