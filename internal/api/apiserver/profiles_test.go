package apiserver_test

import (
	"encoding/json"
	"net/http"
	"strconv"

	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestListQualityProfiles_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)

	recorder := fixture.do(authedGet(t, nil, "/api/v1/qualityprofiles"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for no credential", recorder.Code)
	}
}

func TestCreateQualityProfile_AddsAProfileAdministratorsCanSeeImmediately(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/qualityprofiles",
		`{"name":"Lossless","rules":"{}","upgrade":"{}"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var created struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Name != "Lossless" {
		t.Errorf("Name = %q, want Lossless", created.Name)
	}

	found, ok := fixture.cache.Snapshot().QualityProfile(created.ID)
	if !ok || found.Name != "Lossless" {
		t.Errorf("stored profile = %+v, want Name=Lossless", found)
	}
}

func TestCreateQualityProfile_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/qualityprofiles",
		`{"name":"Lossless","rules":"{}","upgrade":"{}"}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

// The interpreter's own decode error must surface as a 400 with the
// specific reason, not a generic 500 -- ADR-44: a bad policy is refused on
// write, not discovered later at startup.
func TestCreateQualityProfile_InvalidRulesAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/qualityprofiles",
		`{"name":"Broken","rules":"not json","upgrade":"{}"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid rules JSON, body = %s", recorder.Code, recorder.Body)
	}
}

func TestUpdateQualityProfile_ChangesContent(t *testing.T) {
	fixture := newWorksFixture(t)
	created, err := fixture.cache.CreateQualityProfile(t.Context(), "Draft", "{}", "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/qualityprofiles/"+strconv.FormatInt(created.ID, 10),
		`{"name":"Final","rules":"{}","upgrade":"{}"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	found, ok := fixture.cache.Snapshot().QualityProfile(created.ID)
	if !ok || found.Name != "Final" {
		t.Errorf("stored profile = %+v, want Name=Final", found)
	}
}

func TestUpdateQualityProfile_MissingProfileAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/qualityprofiles/999999",
		`{"name":"Final","rules":"{}","upgrade":"{}"}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a missing profile", recorder.Code)
	}
}

func TestDeleteQualityProfile_RemovesIt(t *testing.T) {
	fixture := newWorksFixture(t)
	created, err := fixture.cache.CreateQualityProfile(t.Context(), "Throwaway", "{}", "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/qualityprofiles/"+strconv.FormatInt(created.ID, 10)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if _, ok := fixture.cache.Snapshot().QualityProfile(created.ID); ok {
		t.Error("profile still exists after delete")
	}
}

func TestDeleteQualityProfile_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	created, err := fixture.cache.CreateQualityProfile(t.Context(), "Throwaway", "{}", "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/qualityprofiles/"+strconv.FormatInt(created.ID, 10)))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

func TestCreateLanguageProfile_AddsAProfileAdministratorsCanSeeImmediately(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/languageprofiles",
		`{"name":"English","audio_required":["en"],"prefer_original_audio":true}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var created struct {
		ID                  int64    `json:"id"`
		Name                string   `json:"name"`
		AudioRequired       []string `json:"audio_required"`
		AudioPreferred      []string `json:"audio_preferred"`
		PreferOriginalAudio bool     `json:"prefer_original_audio"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Name != "English" || len(created.AudioRequired) != 1 || created.AudioRequired[0] != "en" {
		t.Errorf("created = %+v, want name=English audio_required=[en]", created)
	}
	if !created.PreferOriginalAudio {
		t.Error("PreferOriginalAudio = false, want true")
	}
	// A field never sent must round-trip as [], not JSON null (the same
	// crash class PersonDetail.Genres/Aliases hit, 2026-09-12).
	if created.AudioPreferred == nil {
		t.Error("AudioPreferred = null, want an empty array")
	}
}

func TestCreateLanguageProfile_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/languageprofiles", `{"name":"English"}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

func TestUpdateLanguageProfile_ChangesContent(t *testing.T) {
	fixture := newWorksFixture(t)
	created, err := fixture.cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{Name: "Draft"})
	if err != nil {
		t.Fatalf("CreateLanguageProfile: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/languageprofiles/"+strconv.FormatInt(created.ID, 10),
		`{"name":"Final","reject_dubbed":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	found, ok := fixture.cache.Snapshot().LanguageProfile(created.ID)
	if !ok || found.Name != "Final" || !found.RejectDubbed {
		t.Errorf("stored profile = %+v, want Name=Final RejectDubbed=true", found)
	}
}

func TestDeleteLanguageProfile_RemovesIt(t *testing.T) {
	fixture := newWorksFixture(t)
	created, err := fixture.cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{Name: "Throwaway"})
	if err != nil {
		t.Fatalf("CreateLanguageProfile: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/languageprofiles/"+strconv.FormatInt(created.ID, 10)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if _, ok := fixture.cache.Snapshot().LanguageProfile(created.ID); ok {
		t.Error("profile still exists after delete")
	}
}
