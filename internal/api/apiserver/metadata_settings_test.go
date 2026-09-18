package apiserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
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

// The screen asking for a key needs to know whether one is set; the key itself
// must never come back (Invariant 61).
func TestSetAcoustIDAPIKey_StoresItSealedAndReportsOnlyThatItExists(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	before := fixture.do(authedGet(t, cookies, "/api/v1/metadata/settings"))
	if got := acoustIDConfigured(t, before); got {
		t.Error("acoustid_api_key.has_value = true before any key was set")
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/metadata/acoustid/apikey",
		`{"api_key":"the-real-key"}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	after := fixture.do(authedGet(t, cookies, "/api/v1/metadata/settings"))
	if got := acoustIDConfigured(t, after); !got {
		t.Error("acoustid_api_key.has_value = false after storing one")
	}
	if bytes.Contains(after.Body.Bytes(), []byte("the-real-key")) {
		t.Error("the response carries the raw key -- it must never be returned")
	}

	stored, err := fixture.cache.AcoustIDAPIKey()
	if err != nil {
		t.Fatalf("AcoustIDAPIKey: %v", err)
	}
	if string(stored) != "the-real-key" {
		t.Errorf("stored key = %q, want the one that was sent", string(stored))
	}
}

// An empty key clears it, which disables the rung rather than failing it (D12).
func TestSetAcoustIDAPIKey_AnEmptyKeyClearsIt(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.do(authedPost(t, cookies, "/api/v1/metadata/acoustid/apikey", `{"api_key":"a-key"}`))
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/metadata/acoustid/apikey", `{"api_key":"  "}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	after := fixture.do(authedGet(t, cookies, "/api/v1/metadata/settings"))
	if got := acoustIDConfigured(t, after); got {
		t.Error("acoustid_api_key.has_value = true after clearing it")
	}
}

func TestSetAcoustIDAPIKey_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/metadata/acoustid/apikey", `{"api_key":"a-key"}`))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

// With no encryption key decoded the key cannot be sealed, and storing it in
// the clear is not an option (ADR-14).
func TestSetAcoustIDAPIKey_RefusesWithoutSecretsConfigured(t *testing.T) {
	fixture := newWorksFixture(t)
	// EnableSecrets deliberately not called.
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/metadata/acoustid/apikey", `{"api_key":"a-key"}`))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 with no cipher configured", recorder.Code)
	}
}

func acoustIDConfigured(t *testing.T, recorder *httptest.ResponseRecorder) bool {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		AcoustIDAPIKey struct {
			HasValue bool `json:"has_value"`
		} `json:"acoustid_api_key"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body.AcoustIDAPIKey.HasValue
}
