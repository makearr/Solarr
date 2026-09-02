package settings_test

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/makearr/MediaNexus/internal/settings"
)

// The property F9 actually promises: what comes out goes back in and means the
// same thing. An export that cannot be imported is a backup that cannot be
// restored, which is worse than none because it is believed.
func TestDocument_RoundTrips(t *testing.T) {
	cache, _ := newCache(t)

	music := mustCategory(t, cache, "music", "Music", 10)
	anime := mustCategory(t, cache, "tv", "Anime", 20)
	fast := mustRoot(t, cache, "/media/fast", "ssd")
	bulk := mustRoot(t, cache, "/media/bulk", "")

	attach(t, cache, music.ID, fast.ID, 0, 3, true)
	attach(t, cache, music.ID, bulk.ID, 1, 1, false)
	attach(t, cache, anime.ID, bulk.ID, 0, 1, true)

	exported := cache.Export()

	encoded, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded settings.Document
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if problems := decoded.Validate(); len(problems) > 0 {
		t.Fatalf("our own export does not validate: %v", problems)
	}

	// Applied to a *different* install, which is the case that matters: the
	// document carries no identifiers, so it has to rebuild the same shape from
	// natural keys alone.
	fresh, _ := newCache(t)
	if err := fresh.Import(t.Context(), decoded); err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := fresh.Export(); !equalDocuments(got, exported) {
		t.Errorf("round trip changed the configuration:\n got %+v\nwant %+v", got, exported)
	}
}

// A file in version control that reshuffles itself on every export is unusable,
// and that is most of what an export is for.
func TestExport_IsStableAcrossCalls(t *testing.T) {
	cache, _ := newCache(t)
	// The priority ranks are deliberately the reverse of the export order.
	// ListCategories orders by priority_rank, so with ranks that agree with the
	// media type order this test would pass whether or not the exporter sorted
	// anything -- which is exactly how it read before it was checked.
	mustCategory(t, cache, "tv", "Zulu", 10)
	mustCategory(t, cache, "music", "Zemih", 30)
	mustRoot(t, cache, "/media/zzz", "")
	mustRoot(t, cache, "/media/aaa", "")

	first, err := yaml.Marshal(cache.Export())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := yaml.Marshal(cache.Export())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("two exports of the same configuration differ")
	}

	// Stability alone is too weak a claim: the underlying queries already return
	// rows in a fixed order, so a test that only compares two exports passes
	// whether or not this package sorts anything. Asserting the order itself is
	// what makes the ordering this package guarantees actually tested -- and
	// insertion order here is the reverse of it, so the two cannot be confused.
	document := cache.Export()
	if got := []string{document.Roots[0].Path, document.Roots[1].Path}; got[0] != "/media/aaa" {
		t.Errorf("roots exported as %v, want them sorted by path", got)
	}
	if got := document.Categories[0]; got.MediaType != "music" || got.Name != "Zemih" {
		t.Errorf("categories exported starting with %s/%s, want them sorted by media type then name",
			got.MediaType, got.Name)
	}
	if !strings.Contains(string(first), "media_type: music") {
		t.Errorf("export does not look like a document:\n%s", first)
	}
}

// An export is attached to bug reports and committed to repositories. Anything
// in it will eventually be somewhere public.
func TestExport_CarriesNoCredentials(t *testing.T) {
	cache, fake := newCache(t)
	if _, err := cache.CreateUser(t.Context(), fake, "avs", "argon2id$secret-hash", settings.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	mustCategory(t, cache, "music", "Music", 10)

	encoded, err := yaml.Marshal(cache.Export())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for _, forbidden := range []string{"argon2id", "secret-hash", "avs", "password", "api_key"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the export contains %q:\n%s", forbidden, encoded)
		}
	}
}

func TestImport_ReportsEveryProblemAtOnce(t *testing.T) {
	document := settings.Document{
		Version: settings.DocumentVersion,
		Roots: []settings.RootEntry{
			{Path: "relative/path"},
			{Path: ""},
		},
		Categories: []settings.CategoryEntry{
			{MediaType: "", Name: "Nameless"},
			{MediaType: "music", Name: "Music", Roots: []settings.AttachedRoot{{Path: "/not/declared"}}},
		},
	}

	problems := document.Validate()
	// Five distinct mistakes; reporting them one per attempt is five round
	// trips through a file the user has to re-upload each time.
	if len(problems) < 4 {
		t.Errorf("reported %d problems, want every one of them: %v", len(problems), problems)
	}
}

func TestImport_AppliesNothingWhenAnythingIsWrong(t *testing.T) {
	cache, _ := newCache(t)

	document := settings.Document{
		Version: settings.DocumentVersion,
		Roots:   []settings.RootEntry{{Path: "/media/good"}},
		Categories: []settings.CategoryEntry{
			{MediaType: "music", Name: "Music"},
			{MediaType: "", Name: "Broken"},
		},
	}

	if err := cache.Import(t.Context(), document); err == nil {
		t.Fatal("import succeeded despite an invalid category")
	}

	// A partial import leaves settings in a state the user never chose and
	// cannot easily undo (section 14.4).
	if roots := cache.Snapshot().RootFolders(); len(roots) != 0 {
		t.Errorf("a rejected import created %d root folders", len(roots))
	}
	if categories := cache.Snapshot().Categories(); len(categories) != 0 {
		t.Errorf("a rejected import created %d categories", len(categories))
	}
}

func TestImport_IsIdempotent(t *testing.T) {
	cache, _ := newCache(t)
	document := settings.Document{
		Version: settings.DocumentVersion,
		Roots:   []settings.RootEntry{{Path: "/media/music"}},
		Categories: []settings.CategoryEntry{{
			MediaType: "music", Name: "Music", PriorityRank: 10,
			Roots: []settings.AttachedRoot{{Path: "/media/music", Weight: 1, AcceptsNew: true}},
		}},
	}

	for attempt := range 3 {
		if err := cache.Import(t.Context(), document); err != nil {
			t.Fatalf("import %d: %v", attempt, err)
		}
	}

	// Applying a document twice is normal -- it is how a configuration is kept
	// in sync -- and must not multiply what it describes.
	if roots := cache.Snapshot().RootFolders(); len(roots) != 1 {
		t.Errorf("%d root folders after three imports, want 1", len(roots))
	}
	if categories := cache.Snapshot().Categories(); len(categories) != 1 {
		t.Errorf("%d categories after three imports, want 1", len(categories))
	}
}

func TestImport_RefusesADocumentFromTheFuture(t *testing.T) {
	// Silently ignoring fields it does not understand would import a subset of
	// what the file says and report success.
	document := settings.Document{Version: settings.DocumentVersion + 1}

	problems := document.Validate()
	if len(problems) == 0 {
		t.Fatal("a newer document version was accepted")
	}
	if !strings.Contains(problems.Error(), "upgrade") {
		t.Errorf("the error should say what to do: %v", problems)
	}
}

func equalDocuments(a, b settings.Document) bool {
	left, leftErr := yaml.Marshal(a)
	right, rightErr := yaml.Marshal(b)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}

func mustCategory(t *testing.T, cache *settings.Cache, mediaType, name string, rank int64) settings.Category {
	t.Helper()
	created, err := cache.CreateCategory(t.Context(), mediaType, name, rank)
	if err != nil {
		t.Fatalf("CreateCategory(%s/%s): %v", mediaType, name, err)
	}
	return created
}

func mustRoot(t *testing.T, cache *settings.Cache, path, label string) settings.RootFolder {
	t.Helper()
	created, err := cache.CreateRootFolder(t.Context(), path, label)
	if err != nil {
		t.Fatalf("CreateRootFolder(%s): %v", path, err)
	}
	return created
}

func attach(t *testing.T, cache *settings.Cache, categoryID, rootID, order, weight int64, acceptsNew bool) {
	t.Helper()
	err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: categoryID, RootFolderID: rootID,
		Order: order, Weight: weight, AcceptsNew: acceptsNew,
	})
	if err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}
}
