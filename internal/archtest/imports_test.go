// Package archtest enforces the dependency rules of architecture §2 as a test,
// so a layering mistake fails the build rather than surviving review.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// allowedInternalImports is the dependency graph of architecture §2, written out.
// A package may import only what is listed for it; every other MediaNexus import
// is a failure. Adding an edge is therefore a deliberate act, and the arrows can
// only point downward because the table is acyclic.
//
// A package missing from this table fails the test — that is intentional. New
// packages state their dependencies here first.
var allowedInternalImports = map[string][]string{
	"cmd/medianexus": {},
	"internal/clock": {},
	"internal/fsx":   {"internal/clock"},
}

// Plugins import internal/plugin and nothing else from core, which is what keeps
// ADR-4's future out-of-process option open.
const pluginDir = "plugins"

var pluginAllowedImports = []string{"internal/plugin"}

// Directories with no Go packages of ours, or with generated or third-party code.
var skippedDirs = []string{".git", ".github", "web", "node_modules", "private", "testdata", "documentation"}

func TestImportGraph_MatchesTheDeclaredLayering(t *testing.T) {
	root := repoRoot(t)
	module := modulePath(t, root)

	for pkg, imports := range packageImports(t, root, module) {
		allowed, declared := allowedFor(pkg)
		if !declared {
			t.Errorf("package %s is not in allowedInternalImports; declare its dependencies there", pkg)
			continue
		}
		for _, imported := range imports {
			if !slices.Contains(allowed, imported) {
				t.Errorf("%s imports %s, which its entry in allowedInternalImports does not permit", pkg, imported)
			}
		}
	}
}

func TestImportGraph_TableListsOnlyPackagesThatExist(t *testing.T) {
	root := repoRoot(t)
	module := modulePath(t, root)
	found := packageImports(t, root, module)

	for pkg := range allowedInternalImports {
		if _, ok := found[pkg]; !ok {
			t.Errorf("allowedInternalImports lists %s, which has no Go package; remove the stale entry", pkg)
		}
	}
}

func allowedFor(pkg string) (allowed []string, declared bool) {
	if pkg == pluginDir || strings.HasPrefix(pkg, pluginDir+"/") {
		return pluginAllowedImports, true
	}
	allowed, declared = allowedInternalImports[pkg]
	return allowed, declared
}

// packageImports maps each package directory, relative to the repository root,
// to the MediaNexus packages it imports. Test files are excluded: a test may
// legitimately reach for fakes and helpers the package itself must not.
func packageImports(t *testing.T, root, module string) map[string][]string {
	t.Helper()
	result := make(map[string][]string)

	err := filepath.WalkDir(root, func(fullPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, fullPath)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if relative != "." && (strings.HasPrefix(entry.Name(), ".") || slices.Contains(skippedDirs, entry.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		pkg := filepath.ToSlash(filepath.Dir(relative))
		if _, seen := result[pkg]; !seen {
			result[pkg] = nil
		}
		for _, imported := range internalImportsOf(t, fullPath, module) {
			if !slices.Contains(result[pkg], imported) {
				result[pkg] = append(result[pkg], imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return result
}

func internalImportsOf(t *testing.T, file, module string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	var internal []string
	for _, spec := range parsed.Imports {
		unquoted, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("import path %s in %s: %v", spec.Path.Value, file, err)
		}
		if unquoted == module {
			internal = append(internal, ".")
			continue
		}
		if strings.HasPrefix(unquoted, module+"/") {
			internal = append(internal, strings.TrimPrefix(unquoted, module+"/"))
		}
	}
	return internal
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}
	return root
}

func modulePath(t *testing.T, root string) string {
	t.Helper()
	content, err := os.ReadFile(path.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	for line := range strings.Lines(string(content)) {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(after)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}
