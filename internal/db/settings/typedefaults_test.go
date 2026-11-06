package settings_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestAMetadataOrderIsTheLibrarysOwnElseTheTypesElseTheShippedOne(t *testing.T) {
	cache, fake := newCache(t)
	movies, err := cache.CreateCategory(t.Context(), "movie", "Movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	order := func() []string {
		snapshot := cache.Snapshot()
		category, _ := snapshot.Category(movies.ID)
		return snapshot.MetadataSourcesFor(category)
	}
	if got := order(); !slices.Equal(got, []string{"wikidata", "tmdb", "radarr", "mdblist", "omdb", "tvdb", "fanart"}) {
		t.Fatalf("shipped order = %v", got)
	}
	if err := cache.SetTypeDefaults(t.Context(), fake, "movie", settings.TypeDefaults{MetadataSources: []string{"radarr", "tmdb"}}); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, []string{"radarr", "tmdb"}) {
		t.Errorf("the type's default = %v, want [radarr tmdb]", got)
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), movies.ID, []string{"fanart", "tmdb"}); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, []string{"fanart", "tmdb"}) {
		t.Errorf("the library's own = %v, want [fanart tmdb]", got)
	}
	if err := cache.SetCategoryMetadataSources(t.Context(), movies.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetTypeDefaults(t.Context(), fake, "movie", settings.TypeDefaults{}); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, settings.DefaultMovieSources) {
		t.Errorf("reset type default = %v, want the shipped order", got)
	}
}

func TestATypeDefaultNamesOnlyWhatTheTypeCouldUse(t *testing.T) {
	cache, fake := newCache(t)
	if err := cache.SetTypeDefaults(t.Context(), fake, "movie", settings.TypeDefaults{MetadataSources: []string{"tvmaze"}}); err == nil {
		t.Error("a TV provider is no source for movies")
	}
	missing := int64(9999)
	if err := cache.SetTypeDefaults(t.Context(), fake, "movie", settings.TypeDefaults{TargetID: &missing, LanguageProfileID: &missing}); err == nil {
		t.Error("a target and a language profile that do not exist are refused")
	}
}

func TestQualityIsTheLibrarysOwnElseTheTypesAndAnOwnChoiceKeepsStanding(t *testing.T) {
	cache, fake := newCache(t)
	music, err := cache.CreateCategory(t.Context(), "music", "Music", 0)
	if err != nil {
		t.Fatal(err)
	}
	var target settings.QualityTarget
	for _, one := range cache.Snapshot().QualityTargets() {
		if one.MediaType == settings.TargetFamily("music") && target.ID == 0 {
			target = one
		}
	}
	other := target
	for _, one := range cache.Snapshot().QualityTargets() {
		if one.MediaType == target.MediaType && one.ID != target.ID {
			other = one
			break
		}
	}
	language, err := cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{Name: "type default", AudioPreferred: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := cache.CreateQualityProfile(t.Context(), "type profile", `{"require":{},"compare":["lossless"]}`, `{"min_delta":15}`)
	if err != nil {
		t.Fatal(err)
	}
	current := func() settings.Category {
		category, _ := cache.Snapshot().Category(music.ID)
		return category
	}
	if got := current(); got.QualityTarget != nil || got.LanguageProfileID != nil || got.QualityTargetInherited {
		t.Fatalf("with no default the library has nothing: %+v", got)
	}
	defaults := settings.TypeDefaults{TargetID: &target.ID, LanguageProfileID: &language.ID, QualityProfileIDs: []int64{profile.ID}}
	if err := cache.SetTypeDefaults(t.Context(), fake, "music", defaults); err != nil {
		t.Fatal(err)
	}
	got := current()
	if got.QualityTarget == nil || got.QualityTarget.ID != target.ID || !got.QualityTargetInherited ||
		got.LanguageProfileID == nil || *got.LanguageProfileID != language.ID || !got.LanguageProfileInherited ||
		!slices.Equal(got.QualityProfileIDs, []int64{profile.ID}) || !got.QualityProfilesInherited {
		t.Fatalf("the library takes the type's defaults: %+v", got)
	}
	// Saving the library's name with the values it shows must not turn the inherited ones into its own.
	if _, err := cache.UpdateCategory(t.Context(), music.ID, "Music", 0, got.QualityProfileID, got.LanguageProfileID, "monitored"); err != nil {
		t.Fatal(err)
	}
	if got = current(); !got.QualityProfilesInherited || !got.LanguageProfileInherited {
		t.Errorf("a rename made the defaults the library's own: %+v", got)
	}
	if other.ID != target.ID {
		if err := cache.SetCategoryQuality(t.Context(), music.ID, nil, nil, &other.ID); err != nil {
			t.Fatal(err)
		}
		if got = current(); got.QualityTarget == nil || got.QualityTarget.ID != other.ID || got.QualityTargetInherited {
			t.Errorf("the library's own target loses to the type's: %+v", got)
		}
		if err := cache.SetTypeDefaults(t.Context(), fake, "music", settings.TypeDefaults{}); err != nil {
			t.Fatal(err)
		}
		if got = current(); got.QualityTarget == nil || got.QualityTarget.ID != other.ID {
			t.Errorf("resetting the type left the library without its own target: %+v", got)
		}
	}
	if err := cache.SetCategoryQuality(t.Context(), music.ID, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got = current(); got.QualityTarget != nil || got.LanguageProfileID != nil || len(got.QualityProfileIDs) != 0 {
		t.Errorf("with the type's defaults gone the library has none: %+v", got)
	}
}
