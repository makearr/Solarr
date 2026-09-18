package apiserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

type metadataSettingsBody struct {
	BrainzMashUserAgent string `json:"brainzmash_user_agent"`
}

func TestGetMetadataSettings_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/metadata/settings", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestGetMetadataSettings_DefaultsToTheShippedAgent(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/metadata/settings"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body metadataSettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := (metadataSettingsBody{BrainzMashUserAgent: "Lidarr/2.9.6.4552"}); body != want {
		t.Errorf("body = %+v, want %+v", body, want)
	}
}

func TestUpdateMetadataSettings_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/metadata/settings", `{"brainzmash_user_agent":"Lidarr/3.0.0"}`))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestUpdateMetadataSettings_RoundTrips(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/metadata/settings", `{"brainzmash_user_agent":"Lidarr/3.0.0"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body metadataSettingsBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := (metadataSettingsBody{BrainzMashUserAgent: "Lidarr/3.0.0"}); body != want {
		t.Errorf("body = %+v, want %+v", body, want)
	}
}

func TestUpdateMetadataSettings_RefusesAHeaderInjection(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/metadata/settings", `{"brainzmash_user_agent":"Lidarr/3.0\r\nX-Injected: 1"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
