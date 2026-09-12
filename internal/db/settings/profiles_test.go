package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/identify/score"
)

func TestCreateQualityProfile_RoundTrips(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateQualityProfile(t.Context(), "lossless",
		`{"require":{},"compare":["lossless"]}`, `{"min_delta":15}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	found, ok := cache.Snapshot().QualityProfile(created.ID)
	if !ok {
		t.Fatal("created profile not found in snapshot")
	}
	if found.Name != "lossless" || found.RulesJSON != `{"require":{},"compare":["lossless"]}` {
		t.Errorf("got %+v, want the profile just created", found)
	}
}

func TestCreateQualityProfile_RejectsInvalidJSON(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateQualityProfile(t.Context(), "broken", `{not json`, `{}`)
	if err == nil {
		t.Fatal("CreateQualityProfile succeeded, want it refused: rules is not valid JSON")
	}
}

// A quality profile can be syntactically valid JSON and still not be ADR-40's
// shape -- score.Parse, not just json.Valid, is what has to catch this.
func TestCreateQualityProfile_RejectsValidJSONThatIsNotADR40Shape(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateQualityProfile(t.Context(), "wrong-shape",
		`{"compare":"resolution"}`, `{}`)
	if err == nil {
		t.Fatal("CreateQualityProfile succeeded, want it refused: compare must be a list, not a string")
	}
}

func TestCreateQualityProfile_RejectsEmptyName(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateQualityProfile(t.Context(), "  ", `{}`, `{}`)
	if err == nil {
		t.Fatal("CreateQualityProfile succeeded, want it refused: name is blank")
	}
}

func TestDeleteQualityProfile_RefusedWhileACategoryDefaultsToIt(t *testing.T) {
	cache, _ := newCache(t)

	profile, err := cache.CreateQualityProfile(t.Context(), "lossless", `{}`, `{}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := cache.SetCategoryQualityProfile(t.Context(), category.ID, &profile.ID); err != nil {
		t.Fatalf("SetCategoryQualityProfile: %v", err)
	}

	err = cache.DeleteQualityProfile(t.Context(), profile.ID)
	if err == nil {
		t.Fatal("DeleteQualityProfile succeeded, want it refused: a category still defaults to it")
	}
	var inUse *settings.InUseError
	if !errors.As(err, &inUse) {
		t.Errorf("error = %v, want an InUseError", err)
	}
}

func TestDeleteQualityProfile_SucceedsOnceDetached(t *testing.T) {
	cache, _ := newCache(t)

	profile, err := cache.CreateQualityProfile(t.Context(), "lossless", `{}`, `{}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := cache.SetCategoryQualityProfile(t.Context(), category.ID, &profile.ID); err != nil {
		t.Fatalf("SetCategoryQualityProfile: %v", err)
	}
	if err := cache.SetCategoryQualityProfile(t.Context(), category.ID, nil); err != nil {
		t.Fatalf("SetCategoryQualityProfile(nil): %v", err)
	}

	if err := cache.DeleteQualityProfile(t.Context(), profile.ID); err != nil {
		t.Errorf("DeleteQualityProfile: %v", err)
	}
}

func TestUpdateQualityProfile_ChangesTheStoredRules(t *testing.T) {
	cache, _ := newCache(t)

	profile, err := cache.CreateQualityProfile(t.Context(), "lossless", `{}`, `{}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	if err := cache.UpdateQualityProfile(t.Context(), profile.ID, "lossless",
		`{"compare":["sample_rate"]}`, `{"min_delta":20}`); err != nil {
		t.Fatalf("UpdateQualityProfile: %v", err)
	}

	found, ok := cache.Snapshot().QualityProfile(profile.ID)
	if !ok || found.RulesJSON != `{"compare":["sample_rate"]}` {
		t.Errorf("got %+v, want the updated rules", found)
	}
}

func TestCreateLanguageProfile_RoundTripsEveryField(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{
		Name:                "anime-subbed",
		AudioPreferred:      []string{"ja", "en"},
		SubtitleRequired:    []string{"en"},
		PreferOriginalAudio: true,
		RejectDubbed:        true,
	})
	if err != nil {
		t.Fatalf("CreateLanguageProfile: %v", err)
	}

	found, ok := cache.Snapshot().LanguageProfile(created.ID)
	if !ok {
		t.Fatal("created profile not found in snapshot")
	}
	if got, want := found.AudioPreferred, []string{"ja", "en"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("AudioPreferred = %v, want %v", got, want)
	}
	if got, want := found.SubtitleRequired, []string{"en"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("SubtitleRequired = %v, want %v", got, want)
	}
	if !found.PreferOriginalAudio || !found.RejectDubbed {
		t.Errorf("PreferOriginalAudio/RejectDubbed = %v/%v, want true/true",
			found.PreferOriginalAudio, found.RejectDubbed)
	}
	// AudioRequired was never set: an absent requirement round-trips as nil, not an
	// empty-but-present list, since section 19.8 gives some categories (adult,
	// music) no language requirement at all.
	if found.AudioRequired != nil {
		t.Errorf("AudioRequired = %v, want nil (no requirement)", found.AudioRequired)
	}
}

func TestCreateLanguageProfile_RejectsEmptyName(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{Name: ""})
	if err == nil {
		t.Fatal("CreateLanguageProfile succeeded, want it refused: name is blank")
	}
}

func TestDeleteLanguageProfile_RefusedWhileACategoryDefaultsToIt(t *testing.T) {
	cache, _ := newCache(t)

	profile, err := cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{
		Name: "english", AudioPreferred: []string{"en"},
	})
	if err != nil {
		t.Fatalf("CreateLanguageProfile: %v", err)
	}
	category, err := cache.CreateCategory(t.Context(), "tv", "Series", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := cache.SetCategoryLanguageProfile(t.Context(), category.ID, &profile.ID); err != nil {
		t.Fatalf("SetCategoryLanguageProfile: %v", err)
	}

	err = cache.DeleteLanguageProfile(t.Context(), profile.ID)
	if err == nil {
		t.Fatal("DeleteLanguageProfile succeeded, want it refused: a category still defaults to it")
	}
}

func TestSetCategoryQualityProfile_RejectsUnknownProfile(t *testing.T) {
	cache, _ := newCache(t)

	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	missing := int64(999)
	err = cache.SetCategoryQualityProfile(t.Context(), category.ID, &missing)
	if err == nil {
		t.Fatal("SetCategoryQualityProfile succeeded, want it refused: profile does not exist")
	}
}

func TestCategory_CarriesItsProfileAssignments(t *testing.T) {
	cache, _ := newCache(t)

	quality, err := cache.CreateQualityProfile(t.Context(), "lossless", `{}`, `{}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	language, err := cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{Name: "english"})
	if err != nil {
		t.Fatalf("CreateLanguageProfile: %v", err)
	}
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if err := cache.SetCategoryQualityProfile(t.Context(), category.ID, &quality.ID); err != nil {
		t.Fatalf("SetCategoryQualityProfile: %v", err)
	}
	if err := cache.SetCategoryLanguageProfile(t.Context(), category.ID, &language.ID); err != nil {
		t.Fatalf("SetCategoryLanguageProfile: %v", err)
	}

	found, ok := cache.Snapshot().Category(category.ID)
	if !ok {
		t.Fatal("category not found")
	}
	if found.QualityProfileID == nil || *found.QualityProfileID != quality.ID {
		t.Errorf("QualityProfileID = %v, want %d", found.QualityProfileID, quality.ID)
	}
	if found.LanguageProfileID == nil || *found.LanguageProfileID != language.ID {
		t.Errorf("LanguageProfileID = %v, want %d", found.LanguageProfileID, language.ID)
	}
}

// TestQualityProfile_ShippedDefaultsAreSeeded is todo 4.3's own remaining checklist
// item: four ready-made profiles so a fresh install has something usable before
// anyone opens the profile builder (migrations/settings/0012). Every shipped
// profile must parse as valid ADR-40 shape -- score.Parse/ParseUpgrade, the same
// validator CreateQualityProfile itself runs -- not just be present.
func TestQualityProfile_ShippedDefaultsAreSeeded(t *testing.T) {
	cache, _ := newCache(t)

	profiles := cache.Snapshot().QualityProfiles()
	wantNames := []string{"Lossless preferred", "Lossy 320", "Space conscious", "AAC 160/48kHz/16-bit (m4a)"}
	if len(profiles) != len(wantNames) {
		t.Fatalf("len(profiles) = %d, want %d shipped defaults", len(profiles), len(wantNames))
	}

	byName := make(map[string]settings.QualityProfile, len(profiles))
	for _, profile := range profiles {
		byName[profile.Name] = profile
	}
	for _, name := range wantNames {
		profile, found := byName[name]
		if !found {
			t.Errorf("shipped default %q not found among seeded profiles", name)
			continue
		}
		if _, err := score.Parse([]byte(profile.RulesJSON)); err != nil {
			t.Errorf("%q: rules_json does not parse as ADR-40 shape: %v", name, err)
		}
		if _, err := score.ParseUpgrade([]byte(profile.UpgradeJSON)); err != nil {
			t.Errorf("%q: upgrade_json does not parse: %v", name, err)
		}
	}
}

// TestQualityProfile_AAC16048kHzShippedDefault_RanksAsIntended is the same
// acceptance criterion TestTargetedProfile_ModerateSpecBeatsMaximalSpec proves
// for the profile builder's own worked example: the shipped "AAC 160/48kHz/
// 16-bit (m4a)" default must actually rank an at-target aac/m4a file above a
// FLAC file that overshoots its sample-rate/bit-depth targets, not merely parse.
func TestQualityProfile_AAC16048kHzShippedDefault_RanksAsIntended(t *testing.T) {
	cache, _ := newCache(t)

	profiles := cache.Snapshot().QualityProfiles()
	var rulesJSON string
	for _, profile := range profiles {
		if profile.Name == "AAC 160/48kHz/16-bit (m4a)" {
			rulesJSON = profile.RulesJSON
		}
	}
	if rulesJSON == "" {
		t.Fatal(`shipped default "AAC 160/48kHz/16-bit (m4a)" not found`)
	}

	profile, err := score.Parse([]byte(rulesJSON))
	if err != nil {
		t.Fatalf("score.Parse: %v", err)
	}

	atTarget := score.Facts{File: score.FileFacts{
		Container: "m4a", AudioCodec: "aac", AudioChannels: "2.0",
		OverallBitrate: 160_000, SampleRate: 48000, BitDepth: 16,
	}}
	overshoot := score.Facts{File: score.FileFacts{
		Container: "flac", AudioCodec: "flac", Lossless: true, AudioChannels: "2.0",
		OverallBitrate: 900_000, SampleRate: 192000, BitDepth: 24,
	}}

	atTargetDecision := score.Evaluate(profile, atTarget, nil)
	overshootDecision := score.Evaluate(profile, overshoot, nil)

	winner, _ := model.Compare(atTargetDecision.Quality, overshootDecision.Quality)
	if winner != -1 {
		t.Errorf("Compare winner = %d, want -1 (the at-target aac/m4a file should win the "+
			"user's own worked example, not the overshooting FLAC)", winner)
	}
}
