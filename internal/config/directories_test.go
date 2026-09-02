package config_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/fsx"
)

func configRootedAt(root string) config.Config {
	settings := config.Default()
	settings.Paths = config.Paths{
		ConfigDir: root,
		DataDir:   filepath.Join(root, "data"),
		DBDir:     filepath.Join(root, "data", "db"),
		CacheDir:  filepath.Join(root, "data", "cache"),
		BackupDir: filepath.Join(root, "data", "backups"),
	}
	return settings
}

func TestCheckDirectories_CreatesWhatIsMissing(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/config", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A fresh volume has only the mount point; the rest is ours to make.
	if err := config.CheckDirectories(fsys, configRootedAt("/config")).OrNil(); err != nil {
		t.Fatalf("CheckDirectories: %v", err)
	}
	for _, path := range []string{"/config/data", "/config/data/db", "/config/data/cache", "/config/data/backups"} {
		info, err := fsys.Stat(path)
		if err != nil {
			t.Errorf("Stat(%s): %v", path, err)
			continue
		}
		if !info.IsDir {
			t.Errorf("%s is not a directory", path)
		}
	}
}

func TestCheckDirectories_LeavesNoProbeBehind(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/config", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := config.CheckDirectories(fsys, configRootedAt("/config")).OrNil(); err != nil {
		t.Fatalf("CheckDirectories: %v", err)
	}

	entries, err := fsys.ReadDir("/config/data/db")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the write probe left %v behind", entries)
	}
}

func TestCheckDirectories_ReportsAPathThatIsAFile(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/config/data", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := fsys.Create("/config/data/db")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	problems := config.CheckDirectories(fsys, configRootedAt("/config"))
	problem, found := findProblem(problems, "paths.db_dir")
	if !found {
		t.Fatalf("no problem reported for a file where a directory belongs; got %v", problems)
	}
	if !strings.Contains(problem.Expectation, "not a directory") {
		t.Errorf("Expectation = %q, want it to say the path is not a directory", problem.Expectation)
	}
}

func TestCheckDirectories_ReportsEveryUnusableDirectoryAtOnce(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/config/data", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, path := range []string{"/config/data/db", "/config/data/cache"} {
		file, err := fsys.Create(path)
		if err != nil {
			t.Fatalf("Create(%s): %v", path, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	problems := config.CheckDirectories(fsys, configRootedAt("/config"))
	for _, setting := range []string{"paths.db_dir", "paths.cache_dir"} {
		if _, found := findProblem(problems, setting); !found {
			t.Errorf("no problem for %s; got %v", setting, problems)
		}
	}
}

func TestCheckDirectories_NamesTheProcessIdentityOnFailure(t *testing.T) {
	// It is nearly always a PUID/PGID mismatch, so the ids are worth saying even
	// without the directory's own ownership (see documentation/startup-checks.md).
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	// No /config at all, and MkdirAll on a Mem filesystem whose root exists will
	// succeed — so force a failure by making the parent a file.
	if err := fsys.MkdirAll("/", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := fsys.Create("/config")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	problems := config.CheckDirectories(fsys, configRootedAt("/config"))
	if len(problems) == 0 {
		t.Fatal("a file where the config directory belongs was accepted")
	}
	rendered := problems.Error()
	if strings.Contains(rendered, "could not be created") && !strings.Contains(rendered, "uid=") {
		t.Errorf("a creation failure does not name the process identity:\n%s", rendered)
	}
}

func TestCheckDirectories_PassesOnARealFilesystem(t *testing.T) {
	// The Mem filesystem cannot model a read-only mount, so this at least proves
	// the probe works against the real thing.
	fsys := fsx.NewOS()
	root, err := fsys.Resolve(t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := config.CheckDirectories(fsys, configRootedAt(root)).OrNil(); err != nil {
		t.Fatalf("CheckDirectories on a real directory: %v", err)
	}

	entries, err := fsys.ReadDir(filepath.Join(root, "data", "db"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the write probe left %v behind", entries)
	}
}

func TestDirectoryRequirements_CoverEveryConfiguredPath(t *testing.T) {
	// A path added to config.Paths but not checked is a volume that fails at the first import
	// instead of at startup, which is the failure §14.3 exists to prevent.
	settings := configRootedAt("/config")
	required := make(map[string]bool)
	for _, requirement := range settings.DirectoryRequirements() {
		required[requirement.Setting] = true
	}

	for _, setting := range []string{
		"paths.config_dir", "paths.data_dir", "paths.db_dir", "paths.cache_dir", "paths.backup_dir",
	} {
		if !required[setting] {
			t.Errorf("%s is configurable but never checked", setting)
		}
	}
}

func TestCheckDirectories_ProblemsAreOrNilWhenClean(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/config", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	problems := config.CheckDirectories(fsys, configRootedAt("/config"))
	if err := problems.OrNil(); err != nil {
		t.Errorf("OrNil() = %v, want nil", err)
	}
	var typed config.Problems
	if errors.As(problems.OrNil(), &typed) {
		t.Error("OrNil returned a typed nil, which reads as non-nil at a call site")
	}
}
