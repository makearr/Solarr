package apiserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// fakeIndexerKinds offers a Newznab preset, a public tracker and Soulseek.
type fakeIndexerKinds struct{}

func (fakeIndexerKinds) IndexerKinds() []apiserver.IndexerKind {
	return []apiserver.IndexerKind{
		{ID: "nzbgeek", Name: "NZBgeek", Impl: "newznab", Protocol: "usenet", Privacy: "private", BaseURLs: []string{"https://api.nzbgeek.info"},
			MediaTypes: []string{"music", "tv"}, SecretLabel: "API key"},
		{ID: "torznab", Name: "Torznab (generic)", Impl: "torznab", Protocol: "torrent", Privacy: "private"},
		{ID: "thepiratebay", Name: "The Pirate Bay", Impl: "thepiratebay", Protocol: "torrent", Privacy: "public",
			BaseURLs: []string{"https://apibay.org"}, MediaTypes: []string{"music"}},
		{ID: "soulseek", Name: "Soulseek (slskd)", Impl: "soulseek", Protocol: "ddl", Privacy: "semi-private",
			BaseURLs: []string{"http://slskd:5030"}, MediaTypes: []string{"music"}, SecretLabel: "API key", CompanionDownloader: "slskd"},
	}
}

func adminWithSecrets(t *testing.T) (worksFixture, []*http.Cookie) {
	t.Helper()
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	return fixture, fixture.signIn(t, "admin")
}

func TestListIndexerKinds_AnswersTheCatalog(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/indexers/schema"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var kinds []struct {
		ID                  string   `json:"id"`
		Privacy             string   `json:"privacy"`
		BaseURLs            []string `json:"base_urls"`
		CompanionDownloader string   `json:"companion_downloader"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &kinds); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(kinds) != 4 || kinds[0].ID != "nzbgeek" || kinds[1].BaseURLs == nil || kinds[2].Privacy != "public" ||
		kinds[3].CompanionDownloader != "slskd" {
		t.Errorf("kinds = %+v", kinds)
	}
}

// An indexer remembers the catalog entry it came from, and editing it without naming one keeps it.
func TestAddIndexer_KeepsItsDefinitionAndRefusesAnUnknownImpl(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers",
		`{"name":"NZBgeek","impl":"newznab","definition":"nzbgeek","base_url":"https://api.nzbgeek.info","api_key":"k"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created struct {
		ID         int64  `json:"id"`
		Definition string `json:"definition"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &created)
	if created.Definition != "nzbgeek" {
		t.Errorf("definition = %q", created.Definition)
	}
	updated := fixture.do(authedPatch(t, cookies, "/api/v1/indexers/"+itoa(created.ID),
		`{"name":"Geek","impl":"newznab","base_url":"https://api.nzbgeek.info","enabled":false}`))
	if updated.Code != http.StatusOK || !json.Valid(updated.Body.Bytes()) {
		t.Fatalf("update status = %d, body = %s", updated.Code, updated.Body)
	}
	var after struct {
		Definition string `json:"definition"`
	}
	_ = json.Unmarshal(updated.Body.Bytes(), &after)
	if after.Definition != "nzbgeek" {
		t.Errorf("after an edit definition = %q, want it kept", after.Definition)
	}
	if refused := fixture.do(authedPost(t, cookies, "/api/v1/indexers", `{"name":"x","impl":"cardigann","base_url":"https://x"}`)); refused.Code != http.StatusBadRequest {
		t.Errorf("an unknown impl: status %d", refused.Code)
	}
}

// Soulseek and slskd each offer the other, built from the stored address and key.
func TestCompanions_AddTheOtherHalfOfSoulseek(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers",
		`{"name":"Soulseek","impl":"soulseek","definition":"soulseek","base_url":"http://slskd:5030","api_key":"the-key"}`))
	var indexer struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &indexer)
	added := fixture.do(authedPost(t, cookies, "/api/v1/indexers/"+itoa(indexer.ID)+"/companion", ``))
	if added.Code != http.StatusCreated {
		t.Fatalf("indexer companion: status %d, body %s", added.Code, added.Body)
	}
	var client struct {
		ID       int64  `json:"id"`
		Impl     string `json:"impl"`
		BaseURL  string `json:"base_url"`
		Protocol string `json:"protocol"`
	}
	_ = json.Unmarshal(added.Body.Bytes(), &client)
	if client.Impl != "slskd" || client.BaseURL != "http://slskd:5030" || client.Protocol != "ddl" {
		t.Errorf("client = %+v", client)
	}
	key, err := fixture.cache.DecryptedDownloaderCredentials(t.Context(), client.ID)
	if err != nil || key.Reveal() != "the-key" {
		t.Errorf("the client's key = %q, %v; want the indexer's", key.Reveal(), err)
	}

	back := fixture.do(authedPost(t, cookies, "/api/v1/downloaders/"+itoa(client.ID)+"/companion", ``))
	if back.Code != http.StatusCreated {
		t.Fatalf("downloader companion: status %d, body %s", back.Code, back.Body)
	}
	var searcher struct {
		Impl       string `json:"impl"`
		Definition string `json:"definition"`
	}
	_ = json.Unmarshal(back.Body.Bytes(), &searcher)
	if searcher.Impl != "soulseek" || searcher.Definition != "soulseek" {
		t.Errorf("indexer = %+v", searcher)
	}

	nzb := fixture.do(authedPost(t, cookies, "/api/v1/indexers",
		`{"name":"NZBgeek","impl":"newznab","definition":"nzbgeek","base_url":"https://api.nzbgeek.info","api_key":"k"}`))
	var plain struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(nzb.Body.Bytes(), &plain)
	if refused := fixture.do(authedPost(t, cookies, "/api/v1/indexers/"+itoa(plain.ID)+"/companion", ``)); refused.Code != http.StatusBadRequest {
		t.Errorf("an indexer without a companion: status %d", refused.Code)
	}
}
