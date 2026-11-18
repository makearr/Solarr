package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A lock that serialises resolving one title only works when every caller shares it: the one place that builds a
// keyed lock for resolvers is cmd/solarr/app (locksFor).
func TestResolverLocks_AreBuiltOnlyInTheApp(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(fullPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, fullPath)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && (strings.HasPrefix(entry.Name(), ".") || slices.Contains(skippedDirs, entry.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(relative, "internal/media/") || !strings.HasSuffix(relative, ".go") || strings.HasSuffix(relative, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(source), "keyedlock.Locks{}") {
			t.Errorf("%s builds its own keyedlock.Locks; take the shared one from the config (cmd/solarr/app locksFor)", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}
