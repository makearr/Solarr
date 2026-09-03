package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
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
