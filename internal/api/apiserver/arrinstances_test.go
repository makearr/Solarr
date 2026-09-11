package apiserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/secrets"
)

func testArrCipherKey() []byte { return bytes.Repeat([]byte{0x2a}, 32) }

type arrInstanceBody struct {
	ID      int64  `json:"id"`
	App     string `json:"app"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Enabled bool   `json:"enabled"`
	APIKey  struct {
		HasValue bool `json:"has_value"`
	} `json:"api_key"`
}

func TestAddArrInstance_SealsTheKeyAndNeverReturnsIt(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances",
		`{"app":"lidarr","name":"Home Lidarr","base_url":"http://lidarr.local:8686","api_key":"the-real-key"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created arrInstanceBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.App != "lidarr" || created.Name != "Home Lidarr" || !created.Enabled {
		t.Errorf("created = %+v, want app=lidarr name=%q enabled=true", created, "Home Lidarr")
	}
	if !created.APIKey.HasValue {
		t.Error("api_key.has_value = false, want true")
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("the-real-key")) {
		t.Error("response body contains the raw api key -- it must never be echoed (Invariant 61)")
	}

	listRecorder := fixture.do(authedGet(t, cookies, "/api/v1/arr/instances"))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRecorder.Code, listRecorder.Body)
	}
	var listed []arrInstanceBody
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Errorf("listed = %+v, want exactly the one created", listed)
	}
}

func TestAddArrInstance_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/arr/instances",
		`{"app":"lidarr","name":"Home Lidarr","base_url":"http://lidarr.local:8686","api_key":"key"}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated add", recorder.Code)
	}
}

// Without EnableSecrets (an encryption key that never decoded), adding a
// connection must refuse rather than ever store the key unencrypted.
func TestAddArrInstance_RefusesWithoutSecretsConfigured(t *testing.T) {
	fixture := newWorksFixture(t)
	// EnableSecrets deliberately not called.
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances",
		`{"app":"lidarr","name":"Home Lidarr","base_url":"http://lidarr.local:8686","api_key":"key"}`))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 with no cipher configured", recorder.Code)
	}
}

func TestUpdateArrInstance_NeverTouchesTheKey(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	addRecorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances",
		`{"app":"lidarr","name":"Home Lidarr","base_url":"http://lidarr.local:8686","api_key":"key"}`))
	var created arrInstanceBody
	if err := json.Unmarshal(addRecorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	updateRecorder := fixture.do(authedPatch(t, cookies, "/api/v1/arr/instances/"+itoa(created.ID),
		`{"name":"Renamed","base_url":"http://lidarr.local:8686","enabled":true}`))
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateRecorder.Code, updateRecorder.Body)
	}
	var updated arrInstanceBody
	if err := json.Unmarshal(updateRecorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Errorf("Name = %q, want %q", updated.Name, "Renamed")
	}

	opened, err := fixture.cache.DecryptedAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedAPIKey: %v", err)
	}
	if opened.Reveal() != "key" {
		t.Errorf("DecryptedAPIKey after update = %q, want the original key untouched", opened.Reveal())
	}
}

func TestDeleteArrInstance_RemovesIt(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	addRecorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances",
		`{"app":"lidarr","name":"Home Lidarr","base_url":"http://lidarr.local:8686","api_key":"key"}`))
	var created arrInstanceBody
	if err := json.Unmarshal(addRecorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	deleteRecorder := fixture.do(authedDelete(t, cookies, "/api/v1/arr/instances/"+itoa(created.ID)))
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteRecorder.Code, deleteRecorder.Body)
	}

	listRecorder := fixture.do(authedGet(t, cookies, "/api/v1/arr/instances"))
	var listed []arrInstanceBody
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("listed after delete = %+v, want none", listed)
	}
}
