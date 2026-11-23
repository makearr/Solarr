package apiserver_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
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
		{ID: "torznab", Name: "Torznab (generic)", Impl: "torznab", Protocol: "torrent", Privacy: "private", Origin: "overridden"},
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

func TestListIndexerKinds_LeavesOutAKindADescriptorSwitchedOff(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	fixture.hidden["indexer/thepiratebay"] = true
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/indexers/schema"))
	var kinds []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &kinds); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, kind := range kinds {
		got = append(got, kind.ID)
	}
	if !slices.Equal(got, []string{"nzbgeek", "torznab", "soulseek"}) {
		t.Errorf("kinds = %v", got)
	}
}

func TestListIndexerKinds_ShowsTheMirrorsADescriptorNamed(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	fixture.tuned["indexer/thepiratebay"] = apiserver.ProviderTune{BaseURLs: []string{"https://mirror.example.org", "https://apibay.org"}}
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/indexers/schema"))
	var kinds []struct {
		ID       string   `json:"id"`
		BaseURLs []string `json:"base_urls"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &kinds); err != nil {
		t.Fatal(err)
	}
	for _, kind := range kinds {
		if kind.ID == "thepiratebay" && !slices.Equal(kind.BaseURLs, []string{"https://mirror.example.org", "https://apibay.org"}) {
			t.Errorf("mirrors = %v", kind.BaseURLs)
		}
		if kind.ID != "thepiratebay" && slices.Contains(kind.BaseURLs, "https://mirror.example.org") {
			t.Errorf("%s got another kind's mirrors", kind.ID)
		}
	}
}

func TestLibraryTypeDefaults_LeavesOutAMetadataProviderADescriptorNarrowedAway(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	listed := func() []string {
		recorder := fixture.do(authedGet(t, cookies, "/api/v1/library-type-defaults"))
		var body struct {
			Types []struct {
				MediaType         string `json:"media_type"`
				MetadataAvailable []struct {
					Key string `json:"key"`
				} `json:"metadata_available"`
			} `json:"types"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, one := range body.Types {
			if one.MediaType == "tv" {
				for _, provider := range one.MetadataAvailable {
					keys = append(keys, provider.Key)
				}
			}
		}
		return keys
	}
	if !slices.Contains(listed(), "tmdb") {
		t.Fatalf("tmdb is not offered to tv before narrowing: %v", listed())
	}
	fixture.tuned["metadata/tmdb"] = apiserver.ProviderTune{MediaTypes: []string{"movie"}}
	if slices.Contains(listed(), "tmdb") {
		t.Errorf("tmdb narrowed to movie is still offered to tv: %v", listed())
	}
}

func TestListMetadataProviders_LeavesOutOneADescriptorSwitchedOff(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	keys := func() []string {
		recorder := fixture.do(authedGet(t, cookies, "/api/v1/metadata/providers"))
		var body struct {
			Providers []struct {
				Key string `json:"key"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var found []string
		for _, provider := range body.Providers {
			found = append(found, provider.Key)
		}
		return found
	}
	if !slices.Contains(keys(), "tvmaze") {
		t.Fatalf("tvmaze is not listed before it is switched off: %v", keys())
	}
	fixture.hidden["metadata/tvmaze"] = true
	if slices.Contains(keys(), "tvmaze") {
		t.Errorf("tvmaze switched off by a descriptor is still listed")
	}
}

func TestMetadataProviderInfo_ACustomProviderWithoutCarriesAnswersAnEmptyListNotNull(t *testing.T) {
	settings.SetCustomMetadataProviders([]settings.MetadataProvider{{Key: "custom_tv", Label: "Custom TV", MediaTypes: []string{"tv"}, Custom: true}})
	t.Cleanup(func() { settings.SetCustomMetadataProviders(nil) })
	fixture, cookies := adminWithSecrets(t)
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library-type-defaults"))
	if !strings.Contains(recorder.Body.String(), `"key":"custom_tv"`) || strings.Contains(recorder.Body.String(), `"carries":null`) {
		t.Errorf("a custom provider without carries must answer an empty list, not null: the interface joins it")
	}
}

func TestListIndexerKinds_MarksASpecTheOwnerReplaced(t *testing.T) {
	fixture, cookies := adminWithSecrets(t)
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/indexers/schema"))
	var kinds []struct {
		ID     string  `json:"id"`
		Origin *string `json:"origin"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &kinds); err != nil {
		t.Fatal(err)
	}
	if kinds[0].Origin != nil || kinds[1].Origin == nil || *kinds[1].Origin != "overridden" {
		t.Errorf("origins = %v, %v", kinds[0].Origin, kinds[1].Origin)
	}
}
