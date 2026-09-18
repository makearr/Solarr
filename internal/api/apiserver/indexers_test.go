package apiserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

type indexerBody struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Impl    string `json:"impl"`
	BaseURL string `json:"base_url"`
	Enabled bool   `json:"enabled"`
	APIKey  struct {
		HasValue bool `json:"has_value"`
	} `json:"api_key"`
}

func TestAddIndexer_SealsTheKeyAndNeverReturnsIt(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers",
		`{"name":"NZBgeek","impl":"newznab","base_url":"https://api.nzbgeek.info","api_key":"the-real-key"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created indexerBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Name != "NZBgeek" || created.Impl != "newznab" || !created.Enabled {
		t.Errorf("created = %+v, want name=NZBgeek impl=newznab enabled=true", created)
	}
	if !created.APIKey.HasValue {
		t.Error("api_key.has_value = false, want true")
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("the-real-key")) {
		t.Error("response body contains the raw api key -- it must never be echoed (Invariant 61)")
	}
}

// A public Torznab endpoint may need no API key at all (ADR-47) -- creating
// one must succeed even with no cipher configured, since nothing needs
// sealing.
func TestAddIndexer_SucceedsWithNoAPIKeyAndNoSecretsConfigured(t *testing.T) {
	fixture := newWorksFixture(t)
	// EnableSecrets deliberately not called.
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers",
		`{"name":"Public Torznab","impl":"torznab","base_url":"https://example.org/torznab"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created indexerBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.APIKey.HasValue {
		t.Error("api_key.has_value = true, want false: no key was given")
	}
}

func TestAddIndexer_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/indexers",
		`{"name":"NZBgeek","impl":"newznab","base_url":"https://api.nzbgeek.info"}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated add", recorder.Code)
	}
}

// A real key, unlike an absent one, does need a cipher to seal it.
func TestAddIndexer_RefusesARealKeyWithoutSecretsConfigured(t *testing.T) {
	fixture := newWorksFixture(t)
	// EnableSecrets deliberately not called.
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers",
		`{"name":"NZBgeek","impl":"newznab","base_url":"https://api.nzbgeek.info","api_key":"key"}`))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 with no cipher configured", recorder.Code)
	}
}

func TestUpdateIndexer_NeverTouchesTheKey(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret("the-key"), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/indexers/"+itoa(created.ID),
		`{"name":"NZBgeek Renamed","impl":"newznab","base_url":"https://api.nzbgeek.info","enabled":false}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	opened, err := fixture.cache.DecryptedIndexerAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedIndexerAPIKey: %v", err)
	}
	if opened.Reveal() != "the-key" {
		t.Errorf("DecryptedIndexerAPIKey = %q, want the original untouched by PATCH", opened.Reveal())
	}
}

func TestSetIndexerAPIKey_EmptyClearsIt(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret("the-key"), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers/"+itoa(created.ID)+"/apikey", `{}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	opened, err := fixture.cache.DecryptedIndexerAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedIndexerAPIKey: %v", err)
	}
	if !opened.IsZero() {
		t.Errorf("DecryptedIndexerAPIKey = %q, want empty after clearing", opened.Reveal())
	}
}

func TestDeleteIndexer_RemovesIt(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret(""), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/indexers/"+itoa(created.ID)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	listRecorder := fixture.do(authedGet(t, cookies, "/api/v1/indexers"))
	var listed []indexerBody
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("listed = %+v, want none after delete", listed)
	}
}

func TestDeleteIndexer_MissingAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/indexers/999"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}
