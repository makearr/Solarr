package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// accessGatekeepers construct or resolve an Access and legitimately read the
// snapshot to do it.
var accessGatekeepers = map[string]bool{
	"authenticate.go": true,
	"login.go":        true,
	"setup.go":        true,
}

// forbiddenInHandlers reads category data straight off the settings snapshot,
// bypassing the filtering Access exists to apply (ADR-27, Invariant 36).
var forbiddenInHandlers = []string{
	"Snapshot().Categories(",
	"Snapshot().Category(",
	"Snapshot().VisibleCategoryIDs(",
	"Snapshot().Permission(",
}

// A handler that reads categories off the snapshot sees every one of them,
// including those the caller must get a 404 for (ADR-27). The filtering belongs
// below the handler, so this asserts handlers never reach past it.
func TestHandlersCannotBypassCategoryFiltering(t *testing.T) {
	directory := filepath.Join("..", "api", "apiserver")

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") || accessGatekeepers[name] {
			continue
		}

		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		checked++

		for _, forbidden := range forbiddenInHandlers {
			if strings.Contains(string(content), forbidden) {
				t.Errorf("apiserver/%s calls %s; ask the Access instead, or the handler "+
					"sees categories the caller may not", name, forbidden)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no handler files were checked; the test would pass vacuously")
	}
}
