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
//
// It distinguishes two kinds of wrong, because they belong to different people.
// A path that is relative, absent or a file is a mistake in what was typed, and
// is refused. A path that exists but this process cannot write is a statement
// about ownership on the host, and that is the operator's to decide -- it is
// reported, and accepted.
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

// Writable reports whether the process can write here, and says who owns it if
// it cannot.
//
// MediaNexus does not chown a library root. It holds the 500k files of ADR-22
// and it is the user's, not ours -- so a mismatch is described rather than
// corrected, with everything needed to fix it in one line.
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
	// A probe left behind would litter every library root the user adds.
	return r.FS.Remove(probe)
}
