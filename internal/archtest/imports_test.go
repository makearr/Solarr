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
	"unicode"
)

// allowedInternalImports is the dependency graph of architecture §2, written out.
var allowedInternalImports = map[string][]string{
	"cmd/medianexus": {
		"internal/apiserver", "internal/auth", "internal/clock", "internal/config",
		"internal/egress", "internal/events", "internal/fsx", "internal/health",
		"internal/httpx", "internal/jobs", "internal/library", "internal/mediatool",
		"internal/metacache", "internal/music", "internal/musicbrainz", "internal/paginate",
		"internal/capacity", "internal/privilege", "internal/probe", "internal/recovery",
		"internal/scan", "internal/settings", "internal/store", "internal/webui",
		"internal/wikidata", "migrations",
	},
	"internal/auth":  {"internal/clock", "internal/secrets"},
	"internal/authz": {"internal/settings"},
	"internal/apiserver": {
		"internal/apiserver/gen", "internal/auth", "internal/authz", "internal/clock",
		"internal/config", "internal/events", "internal/fsx", "internal/jobs",
		"internal/model", "internal/paginate", "internal/parse", "internal/privilege",
		"internal/score", "internal/settings",
	},
	"internal/clock":  {},
	"internal/config": {"internal/fsx", "internal/privilege"},
	"internal/events": {"internal/clock"},
	"internal/fsx":    {"internal/clock"},
	"internal/httpx":  {"internal/clock"},
	"internal/health": {"internal/store"},
	"internal/jobs": {
		"internal/clock", "internal/paginate", "internal/store", "internal/store/gen/jobsdb",
	},
	// Builds a library.db at ADR-22 scale for the ADR-28 and ADR-5 benchmarks.
	// Pure domain types (architecture section 6): no I/O, so it depends on nothing
	// of ours.
	"internal/model": {},
	// ADR-18's parser: pure string-to-struct, no I/O, so it depends on nothing but
	// the attribute vocabulary it shares with future scorers via model.Attributes.
	"internal/parse": {"internal/model"},
	// ADR-40's quality/language interpreter: pure rule evaluation producing a
	// model.Decision, no I/O. A caller decodes settings.QualityProfile's raw JSON
	// itself and passes the parsed rules in, so this never imports internal/settings.
	"internal/score": {"internal/model"},
	// Section 10.2's weighted-distance identity matcher: pure, no I/O. Produces a
	// model.Decision-shaped explanation the same way internal/score does.
	"internal/match": {"internal/model"},
	// Field-level provider merge (ADR-8): no I/O, the caller fetches rows and
	// priority order.
	"internal/metaresolve": {},
	"internal/library/fixture": {
		"internal/clock", "internal/fsx", "internal/store",
		"internal/store/gen/librarydb", "migrations",
	},
	// Owns librarydb, as jobs owns jobsdb and settings owns settingsdb. fsx is the
	// quick hash's read of a file's head and tail (section 5.2), nothing else here
	// touches the filesystem. model is F29's queue entry shape (model.Identification,
	// the ADR-24 chain's own candidate type) -- model has no dependencies of its own,
	// so this stays a downward arrow like every other entry here.
	"internal/library": {
		"internal/clock", "internal/fsx", "internal/model", "internal/paginate",
		"internal/store", "internal/store/gen/librarydb",
	},
	// The only unprompted outbound call, so it needs the one way out (Invariant 28).
	"internal/egress": {"internal/clock", "internal/httpx"},
	"internal/store":  {"internal/clock", "internal/fsx"},
	// ADR-30's external tools. It runs subprocesses and parses their output; it
	// touches no database and no filesystem of ours.
	"internal/mediatool": {},
	// Section 21.6's resize-and-encode step: pure image processing (stdlib image
	// package plus a pure-Go WebP encoder dependency), no I/O of its own.
	"internal/artwork": {},
	// Section 11.8's path template compiler/renderer: parsing and string
	// building only, no I/O of its own. fsx is the one dependency, for the
	// ADR-14 sanitiser choke point every rendered component passes through.
	"internal/naming": {"internal/fsx"},
	// Section 11.7's root-folder chooser: pure ranking logic over candidates
	// the caller already fetched, no I/O and no settings.db of its own --
	// the same separation internal/score keeps from internal/settings.
	"internal/placement": {},
	// ADR-48's import inspection: walk, classify, group. No media-type
	// knowledge and no database of its own (ADR-23) -- tag reading and
	// segment plausibility are both injected functions, so this never
	// imports internal/mediatool, internal/parse or internal/music. fsx is
	// the one dependency, for the walk itself.
	"internal/inspect": {"internal/fsx"},
	// Section 11.7's capacity-polling job: probes every configured root via
	// fsx and writes what it found back to settings.db.
	"internal/capacity": {"internal/clock", "internal/fsx", "internal/jobs", "internal/settings"},
	// The music media type, direct-wired until stage 9 extracts it as a plugin. Its
	// scan.Consumer needs the sweep's own types (scan), root folder paths (settings),
	// the filesystem seam for the quick hash (fsx) and an injected clock (ADR-17) --
	// but never store or librarydb directly, which stays library's alone to touch.
	// The enrichment job adds musicbrainz (ADR-29) and jobs (its own handler,
	// mirroring scan.Job).
	"internal/music": {
		"internal/artwork", "internal/clock", "internal/fsx", "internal/httpx",
		"internal/jobs", "internal/library", "internal/match", "internal/mediatool",
		"internal/model", "internal/musicbrainz", "internal/naming", "internal/parse",
		"internal/scan", "internal/settings", "internal/wikidata",
	},
	"internal/paginate": {},
	// The section 10.4 sweep: a walk over the filesystem seam and nothing else.
	// What it finds is persisted by the caller, so it touches no database.
	// The sweep itself walks only the fsx seam; the job handler beside it composes
	// the pieces a scan needs, the way internal/recovery does.
	"internal/scan": {
		"internal/clock", "internal/fsx", "internal/jobs", "internal/library",
		"internal/mediatool", "internal/settings",
	},
	// ADR-39's measured-half sweep: its own job handler, the same shape as
	// internal/scan's -- a Roots lookup for path resolution, mediatool to
	// probe with, library to record what it found. Deliberately separate
	// from internal/scan (todo 4.2, confirmed with the user), so it imports
	// no more than scan itself does.
	"internal/probe": {
		"internal/clock", "internal/jobs", "internal/library", "internal/mediatool",
		"internal/settings",
	},
	// ADR-15's post-restore pass spans library.db and jobs.db, so it composes their
	// owning packages rather than reaching into either gen package (section 4.3).
	"internal/recovery": {
		"internal/clock", "internal/jobs", "internal/library", "internal/store",
	},
	"internal/secrets": {},
	// config supplies the path-and-expectation problem shape, which section 14.4
	// requires settings validation to reuse rather than invent a second one. score
	// is ADR-40's rule parser, needed so a quality profile's shape is validated on
	// write, not only its JSON syntax.
	"internal/settings": {
		"internal/clock", "internal/config", "internal/score",
		"internal/store", "internal/store/gen/settingsdb",
	},
	// Generated by sqlc, never hand-edited. Listed rather than exempted, because
	// generated code depending on ours would be worth knowing about.
	"internal/store/gen/settingsdb": {},
	"internal/store/gen/jobsdb":     {},
	"internal/store/gen/librarydb":  {},
	"internal/store/gen/metadatadb": {},
	// cache/metadata.db (ADR-2, ADR-29): deliberately generic, so every metadata
	// provider shares one cache rather than each owning its own database.
	"internal/metacache": {"internal/store", "internal/store/gen/metadatadb"},
	// ADR-29's provider: two Source implementations over httpx, permanent cache
	// in front. No store/gen import of its own -- caching goes through metacache.
	"internal/musicbrainz": {"internal/clock", "internal/httpx", "internal/metacache"},
	// ADR-8's cross-provider identity bridge: a Wikidata QID (musicbrainz.Artist
	// already extracts one) to a biography. No cache of its own -- see the
	// package doc comment.
	"internal/wikidata": {"internal/httpx"},
	// Generated by oapi-codegen from api/openapi.yaml (ADR-31).
	"internal/apiserver/gen": {},
	// Serves the compiled bundle from embed.FS.
	"internal/webui": {},
	// Takes ownership of directories before dropping root, so it needs the
	// filesystem seam and nothing else.
	"internal/privilege": {"internal/fsx"},
	"migrations":         {"internal/store"},
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

// packageImports maps each package directory, relative to the repository root, to the
// MediaNexus packages it imports.
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

// sqlDirectories are read by sqlc as well as by the migration runner.
var sqlDirectories = []string{"queries", "migrations"}

// A multi-byte character anywhere in a SQL file, comments included, makes sqlc
// generate subtly wrong SQL that still compiles: documentation/sql-and-sqlc.md.
func TestSQLFiles_ContainNoMultiByteCharacters(t *testing.T) {
	root := repoRoot(t)

	for _, directory := range sqlDirectories {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				return nil
			}

			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			relative, _ := filepath.Rel(root, path)
			for offset, character := range string(content) {
				if character > unicode.MaxASCII {
					line := 1 + strings.Count(string(content[:offset]), "\n")
					t.Errorf("%s:%d contains %q; sqlc's SQLite engine shifts every following "+
						"statement by one byte per multi-byte rune, silently generating wrong SQL",
						relative, line, character)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", directory, err)
		}
	}
}
