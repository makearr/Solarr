package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/privilege"
)

// directoryPermissions is what a created directory gets. The process umask
// narrows it; the container runs non-root (§20.2).
const directoryPermissions fs.FileMode = 0o755

// probeName is written and removed to prove a directory is usable. It is
// distinctive so a crash between write and remove leaves something obviously
// ours.
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

// DirectoryRequirements lists what §14.3 checks before anything opens. The most
// common containerised failure is a volume the process cannot write, discovered
// at the first import rather than at startup.
func (c Config) DirectoryRequirements() []DirectoryRequirement {
	return []DirectoryRequirement{
		{Setting: "paths.config_dir", Path: c.Paths.ConfigDir, Create: true},
		{Setting: "paths.data_dir", Path: c.Paths.DataDir, Create: true},
		{Setting: "paths.db_dir", Path: c.Paths.DBDir, Create: true},
		{Setting: "paths.cache_dir", Path: c.Paths.CacheDir, Create: true},
		{Setting: "paths.backup_dir", Path: c.Paths.BackupDir, Create: true},
	}
}

// ManagedDirectories are the paths MediaNexus may take ownership of. Never the
// media roots. See documentation/container-runtime.md.
func (c Config) ManagedDirectories() []string {
	requirements := c.DirectoryRequirements()
	paths := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		paths = append(paths, requirement.Path)
	}
	return paths
}

// CheckDirectories proves each directory exists and can be written, by writing
// and removing a file rather than by inspecting permission bits. A mode that
// looks correct still fails on a read-only mount, a full filesystem or a
// container whose user is not who the mount expects, and the probe catches all
// three.
//
// What this does not check, and why, is in documentation/startup-checks.md:
// the directory's own ownership (so Invariant 62 is only half met), and whether
// db_dir is a network filesystem — which is a bad idea and the operator's
// responsibility rather than something MediaNexus detects.
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
			fmt.Sprintf("is not writable: %v%s", err, identityHint())), true
	}
	return Problem{}, false
}

// probeWritable writes a file, renames it into place and removes it — the same
// three operations an import performs, so a directory that passes here cannot
// fail later for a reason this could have caught.
func probeWritable(fsys fsx.FS, directory string) error {
	probe := filepath.Join(directory, probeName)

	if err := fsx.WriteAtomic(fsys, probe, func(w io.Writer) error {
		_, err := w.Write([]byte("mediaNexus write probe\n"))
		return err
	}); err != nil {
		return err
	}
	if err := fsys.Remove(probe); err != nil {
		return fmt.Errorf("wrote %s but could not remove it: %w", probeName, err)
	}
	return nil
}

// identityHint names the user the process is running as. It is nearly always a
// PUID/PGID mismatch, and saying which ids to chown to saves the round trip
// even without the directory's own ownership (§14.3).
func identityHint() string {
	return "\n      " + privilege.IdentityHint()
}
