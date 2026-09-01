package apiserver

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/privilege"
)

// FilesystemRoots checks a candidate library root against a real filesystem.
type FilesystemRoots struct {
	FS fsx.FS
}

// Check proves the path is absolute, exists, is a directory, and can be written.
//
// Writability is proved by writing, not by reading permission bits. A mode that
// looks correct still fails on a read-only mount, a full filesystem, or a
// container whose user is not who the volume expects -- and in a container the
// third is the usual answer (section 14.3).
func (r FilesystemRoots) Check(path string) error {
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

	if err := r.probeWritable(path); err != nil {
		return fmt.Errorf("%q is not writable by this process (%s): %w",
			path, privilege.IdentityHint(), err)
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
	// A probe left behind would litter every library root the user adds.
	return r.FS.Remove(probe)
}
