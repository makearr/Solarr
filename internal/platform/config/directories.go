package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/privilege"
)

// directoryPermissions is what a created directory gets. The process umask
// narrows it; the container runs non-root (§20.2).
const directoryPermissions fs.FileMode = 0o755

// probeName is written and removed to prove a directory is usable.
const probeName = ".mnx-writable"

// DirectoryRequirement is one directory the process needs, and what it needs
// from it.
type DirectoryRequirement struct {
	// Setting is the dotted config path, so a failure points at what to change.
	Setting string
	Path    string
	// Create says the directory may be made if absent. A missing data directory
	// on a fresh volume is normal; a missing config directory is not.
	Create bool
}

// DirectoryRequirements lists what §14.3 checks before anything opens.
func (c Config) DirectoryRequirements() []DirectoryRequirement {
	return []DirectoryRequirement{
		{Setting: "paths.config_dir", Path: c.Paths.ConfigDir, Create: true},
		{Setting: "paths.data_dir", Path: c.Paths.DataDir, Create: true},
		{Setting: "paths.db_dir", Path: c.Paths.DBDir, Create: true},
		{Setting: "paths.cache_dir", Path: c.Paths.CacheDir, Create: true},
		{Setting: "paths.backup_dir", Path: c.Paths.BackupDir, Create: true},
	}
}

// ManagedDirectories are the paths Solarr may take ownership of. Never the
// media roots. See documentation/container-runtime.md.
func (c Config) ManagedDirectories() []string {
	requirements := c.DirectoryRequirements()
	paths := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		paths = append(paths, requirement.Path)
	}
	return paths
}

// CheckDirectories proves each directory exists and can be written, by writing and removing a
// file rather than by inspecting permission bits (Invariant 62).
func CheckDirectories(fsys fsx.FS, config Config) Problems {
	var problems Problems
	for _, requirement := range config.DirectoryRequirements() {
		if problem, failed := checkDirectory(fsys, requirement); failed {
			problems = append(problems, problem)
		}
	}
	return problems
}

func checkDirectory(fsys fsx.FS, requirement DirectoryRequirement) (Problem, bool) {
	info, err := fsys.Stat(requirement.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !requirement.Create {
			return newProblem(requirement.Setting, requirement.Path, "does not exist"), true
		}
		if err := fsys.MkdirAll(requirement.Path, directoryPermissions); err != nil {
			return newProblem(requirement.Setting, requirement.Path,
				fmt.Sprintf("could not be created: %v%s", err, identityHint())), true
		}
	case err != nil:
		return newProblem(requirement.Setting, requirement.Path,
			fmt.Sprintf("could not be inspected: %v%s", err, identityHint())), true
	case !info.IsDir:
		return newProblem(requirement.Setting, requirement.Path, "exists but is not a directory"), true
	}

	if err := probeWritable(fsys, requirement.Path); err != nil {
		return newProblem(requirement.Setting, requirement.Path,
			fmt.Sprintf("is not writable: %v%s", err, ownerHint(fsys, requirement.Path))), true
	}
	return Problem{}, false
}

// probeWritable writes, renames and removes: the three operations an import
// performs, so passing here means an import cannot fail for a reason this misses.
func probeWritable(fsys fsx.FS, directory string) error {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("create write probe name: %w", err)
	}
	probe := filepath.Join(directory, probeName+"-"+hex.EncodeToString(suffix))

	if err := fsx.WriteAtomic(fsys, probe, func(w io.Writer) error {
		_, err := w.Write([]byte("solarr write probe\n"))
		return err
	}); err != nil {
		return err
	}
	if err := fsys.Remove(probe); err != nil {
		return fmt.Errorf("wrote probe %s but could not remove it: %w", probe, err)
	}
	return nil
}

// identityHint names the user the process is running as (§14.3).
func identityHint() string {
	return "\n      " + privilege.IdentityHint()
}

// ownerHint adds the directory's own owner and mode to identityHint when the filesystem can
// report it, closing the other half of Invariant 62 (documentation/startup-checks.md,
// architecture §14.3) -- matching the hint FilesystemRoots.Writable already gives a library
// root that fails the same probe.
func ownerHint(fsys fsx.FS, path string) string {
	info, err := fsys.Stat(path)
	if err != nil || (info.UID < 0 && info.GID < 0) {
		return identityHint()
	}
	return fmt.Sprintf("\n      directory is uid=%d gid=%d mode %04o; %s",
		info.UID, info.GID, info.Mode.Perm(), privilege.IdentityHint())
}
