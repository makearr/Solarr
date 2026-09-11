package apiserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
)

type importSettingsBody struct {
	AutoCommitConfidence float64 `json:"auto_commit_confidence"`
}

func TestGetImportSettings_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/import/settings", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestGetImportSettings_DefaultsToADR48sOwnDefault(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/import/settings"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body importSettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.AutoCommitConfidence != 0.90 {
		t.Errorf("AutoCommitConfidence = %v, want the shipped default 0.90", body.AutoCommitConfidence)
	}
}

func TestUpdateImportSettings_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/import/settings", `{"auto_commit_confidence":0.85}`))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestUpdateImportSettings_ChangesTheThreshold(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/import/settings", `{"auto_commit_confidence":0.85}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body importSettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.AutoCommitConfidence != 0.85 {
		t.Errorf("AutoCommitConfidence = %v, want 0.85", body.AutoCommitConfidence)
	}

	// A fresh read confirms it was actually persisted, not just echoed back.
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/import/settings"))
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.AutoCommitConfidence != 0.85 {
		t.Errorf("AutoCommitConfidence after re-read = %v, want 0.85", body.AutoCommitConfidence)
	}
}

func TestUpdateImportSettings_RejectsAnOutOfRangeConfidence(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/import/settings", `{"auto_commit_confidence":1.5}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}
