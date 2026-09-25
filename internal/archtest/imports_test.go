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
	"cmd/solarr": {
		"cmd/solarr/app", "internal/api/apiserver", "internal/api/auth", "internal/db/settings",
		"internal/db/store", "internal/platform/clock", "internal/platform/config", "internal/platform/fsx",
		"migrations",
	},
	"cmd/solarr/app": {
		"cmd/solarr/adapters", "internal/acquiring/download", "internal/acquiring/indexer", "internal/acquiring/poll",
		"internal/acquiring/sabnzbd",
		"internal/api/apiserver", "internal/api/auth", "internal/api/health",
		"internal/api/webui", "internal/db/library", "internal/db/recovery", "internal/db/settings",
		"internal/db/store", "internal/importing/arrimport", "internal/importing/downloadwatch",
		"internal/importing/importjob", "internal/jobs",
		"internal/jobs/cachebudget", "internal/jobs/capacity", "internal/media/music", "internal/metadata/metacache",
		"internal/metadata/acoustid", "internal/metadata/musicbrainz", "internal/metadata/wikidata",
		"internal/moving/drainjob",
		"internal/moving/mover", "internal/moving/moverjob", "internal/platform/clock", "internal/platform/config",
		"internal/platform/egress", "internal/platform/events", "internal/platform/fsx",
		"internal/platform/httpx", "internal/platform/logbuffer", "internal/platform/mediatool",
		"internal/platform/privilege",
		"internal/platform/secrets", "internal/scanning/probe", "internal/scanning/scan", "migrations",
	},
	"cmd/solarr/adapters": {
		"internal/acquiring/download", "internal/acquiring/indexer", "internal/acquiring/sabnzbd",
		"internal/api/apiserver", "internal/db/library", "internal/db/settings",
		"internal/identify/model", "internal/identify/priority", "internal/importing/arrimport",
		"internal/importing/importjob", "internal/importing/inspect", "internal/jobs",
		"internal/jobs/cachebudget", "internal/media/music", "internal/metadata/musicbrainz",
		"internal/moving/drainjob", "internal/moving/mover", "internal/moving/moverjob",
		"internal/platform/clock", "internal/platform/egress", "internal/platform/fsx",
		"internal/platform/paginate",
	},
	"internal/api/auth":  {"internal/platform/clock", "internal/platform/secrets"},
	"internal/api/authz": {"internal/db/settings"},
	"internal/api/apiserver": {
		"internal/api/apiserver/gen", "internal/api/apiserver/legal", "internal/api/auth", "internal/api/authz",
		"internal/platform/clock", "internal/platform/config", "internal/platform/events", "internal/platform/fsx",
		"internal/jobs", "internal/identify/model", "internal/platform/paginate", "internal/identify/parse",
		"internal/platform/privilege", "internal/identify/score", "internal/db/settings", "internal/platform/secrets",
	},
	"internal/platform/clock": {},
	// A bounded in-memory slog handler (revision 7.16). Stdlib only: it wraps
	// whatever handler it is given and knows nothing about this codebase.
	"internal/platform/logbuffer": {},
	"internal/platform/config":    {"internal/platform/fsx", "internal/platform/privilege"},
	"internal/platform/events":    {"internal/platform/clock"},
	"internal/platform/fsx":       {"internal/platform/clock"},
	"internal/platform/httpx":     {"internal/platform/clock"},
	"internal/api/health":         {"internal/db/store"},
	"internal/jobs": {
		"internal/platform/clock", "internal/platform/paginate", "internal/db/store", "internal/db/store/gen/jobsdb",
	},
	// Builds a library.db at ADR-22 scale for the ADR-28 and ADR-5 benchmarks.
	// Pure domain types (architecture section 6): no I/O, so it depends on nothing
	// of ours.
	"internal/identify/model": {},
	// ADR-18's parser: pure string-to-struct, no I/O, so it depends on nothing but
	// the attribute vocabulary it shares with future scorers via model.Attributes.
	"internal/identify/parse": {"internal/identify/model"},
	// ADR-40's quality/language interpreter: pure rule evaluation producing a
	// model.Decision, no I/O. A caller decodes settings.QualityProfile's raw JSON
	// itself and passes the parsed rules in, so this never imports internal/db/settings.
	"internal/identify/score": {"internal/identify/model"},
	// Section 10.2's weighted-distance identity matcher: pure, no I/O. Produces a
	// model.Decision-shaped explanation the same way internal/identify/score does.
	"internal/identify/match": {"internal/identify/model"},
	// Field-level provider merge (ADR-8): no I/O, the caller fetches rows and
	// priority order.
	"internal/metadata/metaresolve": {},
	"internal/db/library/fixture": {
		"internal/platform/clock", "internal/platform/fsx", "internal/db/store",
		"internal/db/store/gen/librarydb", "migrations",
	},
	// Owns librarydb, as jobs owns jobsdb and settings owns settingsdb. fsx is the
	// quick hash's read of a file's head and tail (section 5.2), nothing else here
	// touches the filesystem. model is F29's queue entry shape (model.Identification,
	// the ADR-24 chain's own candidate type) -- model has no dependencies of its own,
	// so this stays a downward arrow like every other entry here.
	"internal/db/library": {
		"internal/platform/clock", "internal/platform/fsx", "internal/importing/inspect", "internal/importing/pack",
		"internal/identify/model", "internal/platform/paginate", "internal/db/store", "internal/db/store/gen/librarydb",
	},
	// ADR-29's fingerprint rung (D12). Talks to acoustid.org and nothing else:
	// it identifies, and the caller resolves what it names through the ordered
	// metadata sources, so it never reaches a database or another provider.
	"internal/metadata/acoustid": {"internal/platform/httpx"},
	// The only unprompted outbound call, so it needs the one way out (Invariant 28).
	"internal/platform/egress": {"internal/platform/clock", "internal/platform/httpx"},
	"internal/db/store":        {"internal/platform/clock", "internal/platform/fsx"},
	// ADR-30's external tools. It runs subprocesses and parses their output; it
	// touches no database and no filesystem of ours.
	"internal/platform/mediatool": {},
	// Section 21.6's resize-and-encode step: pure image processing (stdlib image
	// package plus a pure-Go WebP encoder dependency), no I/O of its own.
	"internal/platform/artwork": {},
	// Section 11.8's path template compiler/renderer: parsing and string
	// building only, no I/O of its own. fsx is the one dependency, for the
	// ADR-14 sanitiser choke point every rendered component passes through.
	"internal/moving/naming": {"internal/platform/fsx"},
	// Section 11.7's root-folder chooser: pure ranking logic over candidates
	// the caller already fetched, no I/O and no settings.db of its own --
	// the same separation internal/identify/score keeps from internal/db/settings.
	"internal/moving/placement": {},
	// ADR-51's move engine. Plan is pure like internal/moving/placement's own
	// Resolve, reusing placement.RootCandidate rather than a second,
	// independent copy of the same root-policy shape (this project's own
	// established lesson: two copies of one policy shape is how they
	// drift). ApplyOne does real file I/O (fsx) but still touches no
	// database of its own -- RECORD/UNLINK/TIDY stay the caller's job. The
	// job orchestration layer (persistence, resumability) is its own,
	// separate package when it lands, the same split internal/importing/inspect
	// keeps from internal/importing/importjob.
	"internal/moving/mover": {"internal/platform/fsx", "internal/moving/placement"},
	// ADR-48's import inspection: walk, classify, group. No media-type
	// knowledge and no database of its own (ADR-23) -- tag reading and
	// segment plausibility are both injected functions, so this never
	// imports internal/platform/mediatool, internal/identify/parse or internal/media/music. fsx is
	// the one dependency, for the walk itself.
	"internal/importing/inspect": {"internal/platform/fsx"},
	// Watches download folders: lists, checks an entry has settled, enqueues an import.
	"internal/importing/downloadwatch": {
		"internal/db/library", "internal/db/settings", "internal/importing/importjob", "internal/jobs",
		"internal/platform/clock", "internal/platform/fsx",
	},
	// ADR-50's own detection (todo 4.11): pure grouping over caller-supplied
	// candidates, the same "no I/O, no database" separation
	// internal/moving/placement and internal/identify/priority keep. The one
	// dependency is the release-name parser, reused for its own Artist/
	// Discography fields rather than a second, independent parse.
	"internal/importing/pack": {"internal/identify/parse"},
	// ADR-52's own REST client package (todo 4.13): one client per *Arr
	// application, over its documented API and an API key only -- httpx is
	// the one dependency, the same "just a client" shape musicbrainz/
	// wikidata keep. No database of its own, no write back to the source.
	"internal/importing/arrimport": {"internal/platform/httpx"},
	// Stage 5's own Newznab/Torznab protocol client (ADR-37/43/47): one
	// client per indexer connection, over its documented API and an
	// optional API key -- the same "just a client" shape arrimport/
	// musicbrainz/wikidata all keep. httpx is the one dependency; no
	// database of its own, no title parsing or scoring (ADR-18/40 stay
	// the candidate pipeline's own job).
	"internal/acquiring/indexer": {"internal/platform/httpx"},
	// Stage 5's own SABnzbd downloader client (ADR-46/57): the same "just a
	// client" shape as internal/acquiring/indexer, one dependency, no
	// Downloader interface yet since only one implementation exists.
	// The download-client seam (ADR-46): types only, every client implements it.
	"internal/acquiring/download": {},
	// Follows sent downloads to imported and cleaned (section 10.9); importing and searching are
	// supplied by the caller, so no media type is imported here.
	"internal/acquiring/poll": {
		"internal/acquiring/download", "internal/db/library", "internal/jobs", "internal/platform/clock",
		"internal/platform/fsx",
	},
	"internal/acquiring/sabnzbd": {"internal/platform/httpx", "internal/acquiring/download"},
	// ADR-36's composite acquisition priority: pure arithmetic over
	// caller-supplied facts, no I/O and no database of its own -- the same
	// separation internal/identify/score and internal/moving/placement keep from their own
	// callers. Ready ahead of its consumer (stage 5/6's planner, which does
	// not exist yet), the same shape those two packages were before their
	// own callers existed.
	"internal/identify/priority": {},
	// ADR-49's archive support: List (no writes) and Extract (workspace
	// only, via fsx's SafeJoin choke point, Invariant 76). Reading the
	// source archive itself uses the real filesystem directly, the same
	// reasoning internal/platform/mediatool's own real-path subprocess reads
	// already have -- rar/7z multi-volume support needs real sibling
	// files next to the path given, not an abstraction over them.
	"internal/platform/archive": {"internal/platform/fsx"},
	// Section 11.7's capacity-polling job: probes every configured root via
	// fsx and writes what it found back to settings.db.
	"internal/jobs/capacity": {"internal/platform/clock", "internal/platform/fsx", "internal/jobs", "internal/db/settings"},
	// ADR-60's bounded cache: measures and evicts internal/media/music's own flat
	// images directory (fsx) and internal/metadata/metacache's own SQLite-backed
	// cache, then records what it found back to settings.db -- the same
	// probe-then-write-facts shape internal/jobs/capacity already has.
	"internal/jobs/cachebudget": {"internal/platform/clock", "internal/platform/fsx", "internal/metadata/metacache", "internal/db/settings"},
	// The music media type, direct-wired until stage 9 extracts it as a plugin. Its
	// scan.Consumer needs the sweep's own types (scan), root folder paths (settings),
	// the filesystem seam for the quick hash (fsx) and an injected clock (ADR-17) --
	// but never store or librarydb directly, which stays library's alone to touch.
	// The enrichment job adds musicbrainz (ADR-29) and jobs (its own handler,
	// mirroring scan.Job).
	"internal/media/music": {
		"internal/acquiring/indexer", "internal/acquiring/download",
		"internal/platform/artwork", "internal/platform/clock", "internal/platform/fsx",
		"internal/platform/httpx", "internal/importing/inspect", "internal/jobs", "internal/db/library",
		"internal/db/store/gen/librarydb", "internal/identify/match", "internal/platform/mediatool",
		"internal/identify/model", "internal/moving/mover", "internal/moving/moverjob",
		"internal/metadata/acoustid", "internal/metadata/musicbrainz",
		"internal/moving/naming", "internal/moving/placement",
		"internal/identify/parse", "internal/identify/score", "internal/scanning/scan", "internal/db/settings",
		"internal/metadata/wikidata",
	},
	"internal/platform/paginate": {},
	// The section 10.4 sweep: a walk over the filesystem seam and nothing else.
	// What it finds is persisted by the caller, so it touches no database.
	// The sweep itself walks only the fsx seam; the job handler beside it composes
	// the pieces a scan needs, the way internal/db/recovery does.
	"internal/scanning/scan": {
		"internal/platform/clock", "internal/platform/fsx", "internal/jobs", "internal/db/library",
		"internal/platform/mediatool", "internal/db/settings",
	},
	// ADR-39's measured-half sweep: its own job handler, the same shape as
	// internal/scanning/scan's -- a Roots lookup for path resolution, mediatool to
	// probe with, library to record what it found. Deliberately separate
	// from internal/scanning/scan (todo 4.2, confirmed with the user), so it imports
	// no more than scan itself does.
	"internal/scanning/probe": {
		"internal/platform/clock", "internal/jobs", "internal/db/library", "internal/platform/mediatool",
		"internal/db/settings",
	},
	// ADR-48's import.inspect job: internal/importing/inspect for WALK/GROUP/SHAPE,
	// internal/identify/model for the ADR-24 chain's own types (RunChainForFiles,
	// Decide), internal/media/music for the music Identifier implementations and
	// the inspect.Unit-to-FileRef adapters -- the same shape internal/scanning/scan's
	// own job has for its Consumer, generalised to import's own pipeline.
	"internal/importing/importjob": {
		"internal/platform/clock", "internal/platform/fsx", "internal/importing/inspect", "internal/importing/pack",
		"internal/jobs", "internal/db/library", "internal/platform/mediatool", "internal/identify/model",
		"internal/media/music", "internal/platform/events",
	},
	// ADR-51's resumable Apply: claims a move_item, calls mover.ApplyOne, then
	// records/unlinks through internal/db/library. No internal/db/settings import --
	// a cmd/solarr adapter bridges RootFolders the same way importjob's
	// Policy is bridged.
	"internal/moving/moverjob": {
		"internal/platform/clock", "internal/platform/fsx", "internal/jobs", "internal/db/library", "internal/moving/mover",
	},
	// ADR-51's POST /rootfolders/{id}/drain: gathers real facts, calls
	// mover.PlanDrain (which calls internal/moving/placement itself), persists via
	// internal/db/library, and enqueues internal/moving/moverjob. internal/db/settings is
	// imported for its types only (settings.RootFolder/CategoryRoot), the
	// same lightweight use internal/scanning/scan's own Roots interface already makes
	// -- actual cache access stays behind this package's own Roots interface.
	"internal/moving/drainjob": {
		"internal/platform/clock", "internal/jobs", "internal/db/library", "internal/moving/mover",
		"internal/moving/moverjob", "internal/moving/placement", "internal/db/settings",
	},
	// ADR-15's post-restore pass spans library.db and jobs.db, so it composes their
	// owning packages rather than reaching into either gen package (section 4.3).
	"internal/db/recovery": {
		"internal/platform/clock", "internal/jobs", "internal/db/library", "internal/db/store",
	},
	"internal/platform/secrets": {},
	// config supplies the path-and-expectation problem shape, which section 14.4
	// requires settings validation to reuse rather than invent a second one. score
	// is ADR-40's rule parser, needed so a quality profile's shape is validated on
	// write, not only its JSON syntax.
	"internal/db/settings": {
		"internal/platform/clock", "internal/platform/config", "internal/identify/score",
		"internal/platform/secrets", "internal/db/store", "internal/db/store/gen/settingsdb",
	},
	// Generated by sqlc, never hand-edited. Listed rather than exempted, because
	// generated code depending on ours would be worth knowing about.
	"internal/db/store/gen/settingsdb": {},
	"internal/db/store/gen/jobsdb":     {},
	"internal/db/store/gen/librarydb":  {},
	"internal/db/store/gen/metadatadb": {},
	// cache/metadata.db (ADR-2, ADR-29): deliberately generic, so every metadata
	// provider shares one cache rather than each owning its own database.
	"internal/metadata/metacache": {"internal/db/store", "internal/db/store/gen/metadatadb"},
	// ADR-29's provider: two Source implementations over httpx, permanent cache
	// in front. No store/gen import of its own -- caching goes through metacache.
	"internal/metadata/musicbrainz": {"internal/platform/clock", "internal/platform/httpx", "internal/metadata/metacache"},
	// ADR-8's cross-provider identity bridge: a Wikidata QID (musicbrainz.Artist
	// already extracts one) to a biography. No cache of its own -- see the
	// package doc comment.
	"internal/metadata/wikidata": {"internal/platform/httpx"},
	// Generated by oapi-codegen from api/openapi.yaml (ADR-31).
	"internal/api/apiserver/gen": {},
	// Embedded copies of LICENSE/ATTRIBUTION.md for the About page (section 13);
	// `make license-check` guards against drift. No internal deps of its own.
	"internal/api/apiserver/legal": {},
	// Serves the compiled bundle from embed.FS.
	"internal/api/webui": {},
	// Takes ownership of directories before dropping root, so it needs the
	// filesystem seam and nothing else.
	"internal/platform/privilege": {"internal/platform/fsx"},
	"migrations":                  {"internal/db/store"},
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
// Solarr packages it imports.
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
