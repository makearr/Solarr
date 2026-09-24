package mover

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Permissions is a library's "Set Permissions" (Sonarr's): modes for what Solarr places, and a
// group; GID -1 leaves the group alone.
type Permissions struct {
	FolderMode fs.FileMode
	FileMode   fs.FileMode
	GID        int
}

// ApplyPermissions sets the file's mode and every folder's between it and rootPath, never the root
// itself: the host's own folders are reported, never corrected (section 20.2).
func ApplyPermissions(fsys fsx.FS, rootPath, fileAbs string, permissions Permissions) error {
	if err := applyTo(fsys, fileAbs, permissions.FileMode, permissions.GID); err != nil {
		return err
	}
	root := path.Clean(rootPath)
	for folder := path.Dir(fileAbs); folder != root && strings.HasPrefix(folder, root+"/"); folder = path.Dir(folder) {
		if err := applyTo(fsys, folder, permissions.FolderMode, permissions.GID); err != nil {
			return err
		}
	}
	return nil
}

func applyTo(fsys fsx.FS, target string, mode fs.FileMode, gid int) error {
	if gid >= 0 {
		if err := fsys.Chown(target, -1, gid); err != nil {
			return fmt.Errorf("mover: set group of %q: %w", target, err)
		}
	}
	if err := fsys.Chmod(target, mode); err != nil {
		return fmt.Errorf("mover: set mode of %q: %w", target, err)
	}
	return nil
}
