package settings_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// An export that cannot be imported is a backup that cannot be restored.
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
	if problems := decoded.Validate(netip.Addr{}); len(problems) > 0 {
		t.Fatalf("our own export does not validate: %v", problems)
	}

	// A different install: no identifiers, so natural keys must carry it.
	fresh, freshClock := newCache(t)
	if err := fresh.Import(t.Context(), freshClock, netip.Addr{}, decoded); err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := fresh.Export(); !equalDocuments(got, exported) {
		t.Errorf("round trip changed the configuration:\n got %+v\nwant %+v", got, exported)
	}
}

// Profiles are policy too (ADR-40, section 19.8), so they round-trip the same way
// categories and roots do -- including a category's assignment to one of each.
func TestDocument_RoundTrips_IncludesProfiles(t *testing.T) {
	cache, _ := newCache(t)

	quality, err := cache.CreateQualityProfile(t.Context(), "lossless",
		`{"compare":["lossless"]}`, `{"min_delta":15}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	language, err := cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{
		Name: "english", AudioPreferred: []string{"en"}, AudioRequired: []string{"en", "de"},
	})
	if err != nil {
		t.Fatalf("CreateLanguageProfile: %v", err)
	}
	music := mustCategory(t, cache, "music", "Music", 10)
	if err := cache.SetCategoryQualityProfile(t.Context(), music.ID, &quality.ID); err != nil {
		t.Fatalf("SetCategoryQualityProfile: %v", err)
	}
	if err := cache.SetCategoryLanguageProfile(t.Context(), music.ID, &language.ID); err != nil {
		t.Fatalf("SetCategoryLanguageProfile: %v", err)
	}

	exported := cache.Export()
	encoded, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded settings.Document
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if problems := decoded.Validate(netip.Addr{}); len(problems) > 0 {
		t.Fatalf("our own export does not validate: %v", problems)
	}

	fresh, freshClock := newCache(t)
	if err := fresh.Import(t.Context(), freshClock, netip.Addr{}, decoded); err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := fresh.Export(); !equalDocuments(got, exported) {
		t.Errorf("round trip changed the configuration:\n got %+v\nwant %+v", got, exported)
	}

	category, ok := fresh.Snapshot().Category(fresh.Snapshot().Categories()[0].ID)
	if !ok || category.QualityProfileID == nil || category.LanguageProfileID == nil {
		t.Fatalf("imported category did not carry its profile assignments: %+v", category)
	}
}

// The registry's own 15 scalar settings (todo 4.14) now round-trip too --
// non-default values, or a bug that only zeroed everything back to its
// shipped default would pass silently.
func TestDocument_RoundTrips_IncludesScalarSettings(t *testing.T) {
	cache, fake := newCache(t)

	if err := cache.SetAuthRequired(t.Context(), fake, false); err != nil {
		t.Fatalf("SetAuthRequired: %v", err)
	}
	if err := cache.SetImportAutoCommitConfidence(t.Context(), fake, 0.75); err != nil {
		t.Fatalf("SetImportAutoCommitConfidence: %v", err)
	}
	if err := cache.SetNetworkSettings(t.Context(), fake, settings.NetworkSettings{
		Port: 8443, ProxyURL: "socks5://proxy.internal:1080", ProxyBypass: []string{"10.0.0.0/8"},
	}, netip.Addr{}); err != nil {
		t.Fatalf("SetNetworkSettings: %v", err)
	}
	if err := cache.SetCacheBudget(t.Context(), fake, settings.CacheBudget{
		MaxBytes: 5 << 30, MaxAgeDays: 14, ImagesSharePercent: 60, MetadataSharePercent: 40,
	}); err != nil {
		t.Fatalf("SetCacheBudget: %v", err)
	}
	if err := cache.SetPrioritySettings(t.Context(), fake, settings.PrioritySettings{
		UserRequestWeight: 50, NewReleaseWeight: 35, GapWeight: 25, UpgradeWeight: 15, NewReleaseDays: 10,
	}); err != nil {
		t.Fatalf("SetPrioritySettings: %v", err)
	}

	exported := cache.Export()
	if exported.Settings == nil {
		t.Fatal("Export did not populate Settings at all")
	}
	if exported.Settings.AuthRequired || exported.Settings.NetworkPort != 8443 ||
		exported.Settings.CacheImagesSharePercent != 60 || exported.Settings.PriorityUserRequestWeight != 50 {
		t.Fatalf("exported settings = %+v, want the values just set", exported.Settings)
	}

	encoded, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded settings.Document
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	fresh, freshClock := newCache(t)
	if err := fresh.Import(t.Context(), freshClock, netip.Addr{}, decoded); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := fresh.Export(); !equalDocuments(got, exported) {
		t.Errorf("round trip changed the configuration:\n got %+v\nwant %+v", got.Settings, exported.Settings)
	}
}

// A category naming a profile the document itself never declares would import
// with a silently missing policy -- the same rule an undeclared root already gets.
func TestImport_RefusesACategoryReferencingAnUndeclaredProfile(t *testing.T) {
	document := settings.Document{
		Version: settings.DocumentVersion,
		Categories: []settings.CategoryEntry{
			{MediaType: "music", Name: "Music", QualityProfile: "lossless"},
		},
	}

	problems := document.Validate(netip.Addr{})
	if len(problems) == 0 {
		t.Fatal("a category referencing an undeclared quality profile was accepted")
	}
}

// A file that reshuffles itself on every export is unusable in version control.
func TestExport_IsStableAcrossCalls(t *testing.T) {
	cache, _ := newCache(t)
	// Reversed against the export order: ListCategories sorts by priority_rank,
	// so agreeing ranks would let this pass without the exporter sorting.
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

	// The order itself, not just stability: the queries are already ordered.
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

// Anything in an export eventually ends up somewhere public.
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

	problems := document.Validate(netip.Addr{})
	// Five mistakes, one upload.
	if len(problems) < 4 {
		t.Errorf("reported %d problems, want every one of them: %v", len(problems), problems)
	}
}

func TestImport_AppliesNothingWhenAnythingIsWrong(t *testing.T) {
	cache, fake := newCache(t)

	document := settings.Document{
		Version: settings.DocumentVersion,
		Roots:   []settings.RootEntry{{Path: "/media/good"}},
		Categories: []settings.CategoryEntry{
			{MediaType: "music", Name: "Music"},
			{MediaType: "", Name: "Broken"},
		},
	}

	if err := cache.Import(t.Context(), fake, netip.Addr{}, document); err == nil {
		t.Fatal("import succeeded despite an invalid category")
	}

	// A partial import leaves a state the user never chose (section 14.4).
	if roots := cache.Snapshot().RootFolders(); len(roots) != 0 {
		t.Errorf("a rejected import created %d root folders", len(roots))
	}
	if categories := cache.Snapshot().Categories(); len(categories) != 0 {
		t.Errorf("a rejected import created %d categories", len(categories))
	}
}

func TestImport_IsIdempotent(t *testing.T) {
	cache, fake := newCache(t)
	document := settings.Document{
		Version: settings.DocumentVersion,
		Roots:   []settings.RootEntry{{Path: "/media/music"}},
		Categories: []settings.CategoryEntry{{
			MediaType: "music", Name: "Music", PriorityRank: 10,
			Roots: []settings.AttachedRoot{{Path: "/media/music", Weight: 1, AcceptsNew: true}},
		}},
	}

	for attempt := range 3 {
		if err := cache.Import(t.Context(), fake, netip.Addr{}, document); err != nil {
			t.Fatalf("import %d: %v", attempt, err)
		}
	}

	// Applying twice is how a configuration is kept in sync.
	if roots := cache.Snapshot().RootFolders(); len(roots) != 1 {
		t.Errorf("%d root folders after three imports, want 1", len(roots))
	}
	if categories := cache.Snapshot().Categories(); len(categories) != 1 {
		t.Errorf("%d categories after three imports, want 1", len(categories))
	}
}

func TestImport_RefusesADocumentFromTheFuture(t *testing.T) {
	// Ignoring unknown fields would import a subset and report success.
	document := settings.Document{Version: settings.DocumentVersion + 1}

	problems := document.Validate(netip.Addr{})
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

// Section 14.4: a value is refused where it is entered, not at the next startup.
func TestCreateCategory_RefusesAnUnknownMediaType(t *testing.T) {
	cache, _ := newCache(t)

	if _, err := cache.CreateCategory(t.Context(), "vhs", "Tapes", 10); err == nil {
		t.Fatal("an unknown media type was accepted")
	} else if !strings.Contains(err.Error(), "media_type") {
		t.Errorf("the error should name the field: %v", err)
	}

	if categories := cache.Snapshot().Categories(); len(categories) != 0 {
		t.Errorf("%d categories were created by a rejected request", len(categories))
	}
}

// A duplicate (media_type, name) must be refused with a named-field error a
// caller can act on, not left to category(media_type, name)'s own raw
// UNIQUE constraint -- writeError has nothing to map that to, so it fell
// through to a bare "the request could not be completed" (found live via
// the setup wizard's own *Arr connect step, submitted twice against the
// same suggested library name).
func TestCreateCategory_RefusesADuplicateNameAndMediaType(t *testing.T) {
	cache, _ := newCache(t)

	if _, err := cache.CreateCategory(t.Context(), "music", "Music", 10); err != nil {
		t.Fatalf("CreateCategory (first): %v", err)
	}

	_, err := cache.CreateCategory(t.Context(), "music", "Music", 20)
	if err == nil {
		t.Fatal("a duplicate (media_type, name) was accepted")
	}
	// Specifically config.Problems, not the raw SQLite UNIQUE constraint
	// error surfacing unhandled -- the latter's own text also happens to
	// contain "category.name", so a plain substring check on err.Error()
	// would pass even with the duplicate check removed entirely.
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("err = %v (%T), want it to unwrap to config.Problems", err, err)
	}
	if len(problems) != 1 || problems[0].Path != "name" {
		t.Errorf("problems = %+v, want exactly one problem on the name field", problems)
	}

	// A different media_type with the same name is not a duplicate.
	if _, err := cache.CreateCategory(t.Context(), "tv", "Music", 30); err != nil {
		t.Errorf("a same-name, different-media_type category was refused: %v", err)
	}

	if categories := cache.Snapshot().Categories(); len(categories) != 2 {
		t.Errorf("%d categories exist, want exactly the 2 that should have been created", len(categories))
	}
}

func TestCreateRootFolder_RefusesARelativePath(t *testing.T) {
	cache, _ := newCache(t)

	if _, err := cache.CreateRootFolder(t.Context(), "media/music", ""); err == nil {
		t.Fatal("a relative path was accepted")
	}
	if roots := cache.Snapshot().RootFolders(); len(roots) != 0 {
		t.Errorf("%d root folders were created by a rejected request", len(roots))
	}
}

// An import must not be able to introduce what the interface would refuse.
func TestImport_AppliesTheSameRulesAsTheAPI(t *testing.T) {
	cache, fake := newCache(t)

	document := settings.Document{
		Version:    settings.DocumentVersion,
		Categories: []settings.CategoryEntry{{MediaType: "vhs", Name: "Tapes"}},
	}
	if err := cache.Import(t.Context(), fake, netip.Addr{}, document); err == nil {
		t.Fatal("an import created a category the interface would refuse")
	}
	if categories := cache.Snapshot().Categories(); len(categories) != 0 {
		t.Errorf("%d categories survived a rejected import", len(categories))
	}
}
