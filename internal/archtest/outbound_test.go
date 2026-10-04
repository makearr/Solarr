package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// outboundBudget is the one file that may build a rate limiter or a circuit breaker for the process.
const outboundBudget = "cmd/solarr/app/outbound.go"

// A second limiter is a second budget for the same provider: two jobs then exceed its pace together (ADR-13).
func TestOutboundBudget_IsBuiltInOnePlace(t *testing.T) {
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
		if !strings.HasSuffix(relative, ".go") || strings.HasSuffix(relative, "_test.go") || relative == outboundBudget {
			return nil
		}
		source, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			return readErr
		}
		for _, constructor := range []string{"httpx.NewLimiter(", "httpx.NewBreaker("} {
			if strings.Contains(string(source), constructor) {
				t.Errorf("%s calls %s...); take a client from process.providerClient instead", relative, constructor)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}
