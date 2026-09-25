package settings_test

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// shippedProfileIDs answers the shipped profiles by name.
func shippedProfileIDs(t *testing.T, cache *settings.Cache) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, profile := range cache.Snapshot().QualityProfiles() {
		ids[profile.Name] = profile.ID
	}
	return ids
}

// The user's own example order leads the list of every profile (migration 0019).
func TestQualityProfiles_ListedInSuggestedOrder(t *testing.T) {
	cache, _ := newCache(t)
	var names []string
	for _, profile := range cache.Snapshot().QualityProfiles() {
		names = append(names, profile.Name)
	}
	want := []string{"Space conscious", "Lossy 320", "Lossless preferred"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestSetCategoryQuality_OrderCutoffAndTarget(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	list := []int64{ids["Lossy 320"], ids["Lossless preferred"]}
	bitrate := int64(256)
	target := &settings.QualityTarget{AudioCodec: "aac", Container: "m4a", BitrateKbps: &bitrate, TolerancePct: 10}

	if err := cache.SetCategoryQuality(t.Context(), music.ID, list, &list[1], target); err != nil {
		t.Fatalf("SetCategoryQuality: %v", err)
	}
	got, _ := cache.Snapshot().Category(music.ID)
	if !reflect.DeepEqual(got.QualityProfileIDs, list) || *got.QualityProfileID != list[0] {
		t.Errorf("list = %v first = %v, want %v with the first as the library's profile",
			got.QualityProfileIDs, got.QualityProfileID, list)
	}
	if got.QualityCutoffID == nil || *got.QualityCutoffID != list[1] || !reflect.DeepEqual(got.QualityTarget, target) {
		t.Errorf("cutoff = %v target = %+v", got.QualityCutoffID, got.QualityTarget)
	}

	// Choosing one profile the old way puts it first and keeps the rest of the list.
	if err := cache.SetCategoryQualityProfile(t.Context(), music.ID, &list[1]); err != nil {
		t.Fatal(err)
	}
	got, _ = cache.Snapshot().Category(music.ID)
	if want := []int64{list[1], list[0]}; !reflect.DeepEqual(got.QualityProfileIDs, want) {
		t.Errorf("after choosing one: %v, want %v", got.QualityProfileIDs, want)
	}
}

func TestSetCategoryQuality_ReportsEveryProblem(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	outside := ids["Lossless preferred"]
	zero := int64(0)

	err := cache.SetCategoryQuality(t.Context(), music.ID,
		[]int64{ids["Lossy 320"], ids["Lossy 320"], 999}, &outside,
		&settings.QualityTarget{AudioCodec: "", Container: "m4a", BitrateKbps: &zero, TolerancePct: 80})
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("err = %v, want config.Problems", err)
	}
	var paths []string
	for _, problem := range problems {
		paths = append(paths, problem.Path)
	}
	want := []string{"profiles[1]", "profiles[2]", "cutoff", "target.audio_codec", "target.bitrate_kbps", "target.tolerance_pct"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("problems at %v, want %v", paths, want)
	}
}

func TestDeleteQualityProfile_RefusedWhileAnyLibraryListsIt(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	if err := cache.SetCategoryQuality(t.Context(), music.ID,
		[]int64{ids["Lossy 320"], ids["Space conscious"]}, nil, nil); err != nil {
		t.Fatal(err)
	}
	var inUse *settings.InUseError
	if err := cache.DeleteQualityProfile(t.Context(), ids["Space conscious"]); !errors.As(err, &inUse) {
		t.Errorf("deleting a fallback profile: err = %v, want InUseError", err)
	}
}

func TestDocument_RoundTrips_IncludesLibraryQuality(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	ids := shippedProfileIDs(t, cache)
	list := []int64{ids["Space conscious"], ids["Lossy 320"]}
	if err := cache.SetCategoryQuality(t.Context(), music.ID, list, &list[1],
		&settings.QualityTarget{AudioCodec: "flac", Container: "flac", TolerancePct: 0}); err != nil {
		t.Fatal(err)
	}

	exported := cache.Export()
	encoded, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var decoded settings.Document
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	fresh, freshClock := newCache(t)
	if err := fresh.Import(t.Context(), freshClock, netip.Addr{}, decoded); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := fresh.Export(); !equalDocuments(got, exported) {
		t.Errorf("round trip changed the configuration:\n got %+v\nwant %+v", got, exported)
	}
}

// A document from before libraries held a list still imports, as a list of one.
func TestImport_ASingleQualityProfileBecomesAListOfOne(t *testing.T) {
	source, _ := newCache(t)
	mustCategory(t, source, "music", "Music", 10)
	document := source.Export()
	document.Categories[0].QualityProfile = "Lossy 320"
	document.Categories[0].QualityProfiles = nil

	cache, cl := newCache(t)
	if err := cache.Import(t.Context(), cl, netip.Addr{}, document); err != nil {
		t.Fatalf("import: %v", err)
	}
	category := cache.Snapshot().Categories()[0]
	if want := []int64{shippedProfileIDs(t, cache)["Lossy 320"]}; !reflect.DeepEqual(category.QualityProfileIDs, want) {
		t.Errorf("list = %v, want %v", category.QualityProfileIDs, want)
	}
}

// A library lists only profiles of its own media type (2026-09-25).
func TestSetCategoryQuality_RefusesAnotherMediaTypesProfile(t *testing.T) {
	cache, _ := newCache(t)
	music := mustCategory(t, cache, "music", "Music", 10)
	tv, err := cache.CreateQualityProfileFor(t.Context(), "tv", "HD", `{"quality_profile":"x","compare":["score"],"tiers":{},"targets":{}}`, "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfileFor: %v", err)
	}
	if tv.MediaType != "tv" {
		t.Errorf("MediaType = %q, want tv", tv.MediaType)
	}
	err = cache.SetCategoryQuality(t.Context(), music.ID, []int64{tv.ID}, nil, nil)
	var problems config.Problems
	if !errors.As(err, &problems) || len(problems) != 1 || problems[0].Expectation != "a music quality profile" {
		t.Errorf("err = %v, want one problem asking for a music profile", err)
	}
	if _, err := cache.CreateQualityProfileFor(t.Context(), "vinyl", "x", `{"quality_profile":"x","compare":["score"],"tiers":{},"targets":{}}`, "{}"); err == nil {
		t.Error("created a profile for an unknown media type")
	}
}
