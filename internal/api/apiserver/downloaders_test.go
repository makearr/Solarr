package apiserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

type downloaderBody struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Impl        string `json:"impl"`
	BaseURL     string `json:"base_url"`
	Enabled     bool   `json:"enabled"`
	Protocol    string `json:"protocol"`
	Credentials struct {
		HasValue bool `json:"has_value"`
	} `json:"credentials"`
}

func TestAddDownloader_SealsTheCredentialsAndNeverReturnsThem(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders",
		`{"name":"SABnzbd","impl":"sabnzbd","base_url":"http://sabnzbd.local:8080","protocol":"usenet","credentials":"the-real-key"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created downloaderBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Name != "SABnzbd" || created.Impl != "sabnzbd" || created.Protocol != "usenet" || !created.Enabled {
		t.Errorf("created = %+v, want name=SABnzbd impl=sabnzbd protocol=usenet enabled=true", created)
	}
	if !created.Credentials.HasValue {
		t.Error("credentials.has_value = false, want true")
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("the-real-key")) {
		t.Error("response body contains the raw credential -- it must never be echoed (Invariant 61)")
	}
}

func TestAddDownloader_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/downloaders",
		`{"name":"SABnzbd","impl":"sabnzbd","base_url":"http://sabnzbd.local:8080","protocol":"usenet"}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated add", recorder.Code)
	}
}

func TestAddDownloader_RefusesAnUnknownProtocol(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders",
		`{"name":"SABnzbd","impl":"sabnzbd","base_url":"http://sabnzbd.local:8080","protocol":"ftp"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unknown protocol", recorder.Code)
	}
}

// ADR-57's queue depth holds everything at 0 and nothing useful past 1000: refused on write.
func TestAddDownloader_RefusesAQueueDepthOutOfRange(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	for _, depth := range []string{"0", "1001"} {
		recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders",
			`{"name":"SABnzbd","impl":"sabnzbd","base_url":"http://sabnzbd.local:8080","protocol":"usenet",`+
				`"options":{"max_queued":`+depth+`}}`))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("max_queued %s: status = %d, want 400", depth, recorder.Code)
		}
	}
}

func TestUpdateDownloader_NeverTouchesTheCredentials(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://sabnzbd.local:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret("the-key"), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/downloaders/"+itoa(created.ID),
		`{"name":"SABnzbd Renamed","impl":"sabnzbd","base_url":"http://sabnzbd.local:8080","protocol":"usenet","enabled":false}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	opened, err := fixture.cache.DecryptedDownloaderCredentials(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedDownloaderCredentials: %v", err)
	}
	if opened.Reveal() != "the-key" {
		t.Errorf("DecryptedDownloaderCredentials = %q, want the original untouched by PATCH", opened.Reveal())
	}
}

func TestSetDownloaderCredentials_EmptyClearsThem(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://sabnzbd.local:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret("the-key"), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders/"+itoa(created.ID)+"/credentials", `{}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	opened, err := fixture.cache.DecryptedDownloaderCredentials(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedDownloaderCredentials: %v", err)
	}
	if !opened.IsZero() {
		t.Errorf("DecryptedDownloaderCredentials = %q, want empty after clearing", opened.Reveal())
	}
}

func TestDeleteDownloader_RemovesIt(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://sabnzbd.local:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret(""), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/downloaders/"+itoa(created.ID)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	listRecorder := fixture.do(authedGet(t, cookies, "/api/v1/downloaders"))
	var listed []downloaderBody
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("listed = %+v, want none after delete", listed)
	}
}

func TestDeleteDownloader_MissingAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/downloaders/999"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// fakeDownloaderKinds offers one client with an address and one blackhole.
type fakeDownloaderKinds struct{}

func (fakeDownloaderKinds) DownloaderKinds() []apiserver.DownloaderKind {
	return []apiserver.DownloaderKind{
		{Impl: "sabnzbd", Label: "SABnzbd", Protocol: "usenet", AddressPlaceholder: "http://sabnzbd:8080", SecretLabel: "API key", Categories: true},
		{Impl: "qbittorrent", Label: "qBittorrent", Protocol: "torrent", AddressPlaceholder: "http://qbittorrent:8080", UserName: true, SecretLabel: "Password"},
		{Impl: "slskd", Label: "slskd (Soulseek)", Protocol: "ddl", AddressPlaceholder: "http://slskd:5030", SecretLabel: "API key",
			CompanionIndexer: "soulseek"},
		{Impl: "torrentblackhole", Label: "Torrent Blackhole", Protocol: "torrent", Fields: []apiserver.DownloaderField{
			{Key: "drop_folder", Kind: "path", Label: "Torrent folder", Required: true},
			{Key: "watch_folder", Kind: "path", Label: "Watch folder", Required: true},
		}},
	}
}

func TestListDownloaderKinds_AnswersEveryKindWithItsFields(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	recorder := fixture.do(authedGet(t, fixture.signIn(t, "admin"), "/api/v1/downloaders/schema"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var kinds []struct {
		Impl       string `json:"impl"`
		HasAddress bool   `json:"has_address"`
		UserName   bool   `json:"user_name"`
		Fields     []struct {
			Key      string `json:"key"`
			Required bool   `json:"required"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &kinds); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(kinds) != 4 || kinds[0].Impl != "sabnzbd" || !kinds[0].HasAddress || !kinds[1].UserName || kinds[3].HasAddress ||
		len(kinds[3].Fields) != 2 || !kinds[3].Fields[0].Required {
		t.Errorf("kinds = %+v", kinds)
	}
}

// Each kind needs what it needs: an address, its own protocol, a blackhole's folders; an unknown kind is refused.
func TestAddDownloader_ChecksWhatItsKindNeeds(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	cases := []struct {
		name, body string
		want       int
	}{
		{"an unknown kind", `{"name":"x","impl":"putio","base_url":"http://p","protocol":"torrent"}`, http.StatusBadRequest},
		{"no address", `{"name":"x","impl":"sabnzbd","base_url":"","protocol":"usenet"}`, http.StatusBadRequest},
		{"another protocol", `{"name":"x","impl":"qbittorrent","base_url":"http://q","protocol":"usenet"}`, http.StatusBadRequest},
		{"a blackhole without its watch folder", `{"name":"x","impl":"torrentblackhole","base_url":"","protocol":"torrent",
			"options":{"drop_folder":"/drop"}}`, http.StatusBadRequest},
		{"a blackhole with both folders", `{"name":"x","impl":"torrentblackhole","base_url":"","protocol":"torrent",
			"options":{"drop_folder":"/drop","watch_folder":"/done"}}`, http.StatusCreated},
	}
	for _, test := range cases {
		if recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders", test.body)); recorder.Code != test.want {
			t.Errorf("%s: status = %d, want %d (%s)", test.name, recorder.Code, test.want, recorder.Body)
		}
	}
}
