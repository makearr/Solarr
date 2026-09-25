package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// fakeConnectionTester serves both IndexerTester and DownloaderTester --
// the same shape, so one fake covers both without duplicating it.
type fakeConnectionTester struct {
	result apiserver.ConnectionTestResult
	err    error

	lastIndexerID    int64
	lastDownloaderID int64
	lastConfig       string
}

func (f *fakeConnectionTester) TestDownloaderConfig(
	_ context.Context, impl, baseURL, credentials string, options settings.DownloaderOptions,
) (apiserver.ConnectionTestResult, error) {
	f.lastConfig = impl + " " + baseURL + " " + credentials + " " + options.CategoryFor("music")
	return f.result, f.err
}

func (f *fakeConnectionTester) TestIndexer(_ context.Context, id int64) (apiserver.ConnectionTestResult, error) {
	f.lastIndexerID = id
	return f.result, f.err
}

func (f *fakeConnectionTester) TestDownloader(_ context.Context, id int64) (apiserver.ConnectionTestResult, error) {
	f.lastDownloaderID = id
	return f.result, f.err
}

func TestTestIndexer_ReturnsTheTesterResult(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret(""), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}
	fixture.indexerTester.result = apiserver.ConnectionTestResult{
		OK: true, Message: "Connected. 12 categories, 3 search modes.", TestedAt: fixture.clock.Now(),
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers/"+itoa(created.ID)+"/test", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.indexerTester.lastIndexerID != created.ID {
		t.Errorf("lastIndexerID = %d, want %d", fixture.indexerTester.lastIndexerID, created.ID)
	}
	if !contains(recorder.Body.String(), "12 categories") {
		t.Errorf("body = %s, want the tester's own message", recorder.Body.String())
	}
}

// A connection that answers but not usefully is still a 200 -- ok:false in
// the body, not an HTTP error. The distinction matters: a 4xx/5xx here would
// look like Solarr's own request failed, not the indexer's answer.
func TestTestIndexer_UnreachableIsA200WithOKFalse(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret(""), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}
	fixture.indexerTester.result = apiserver.ConnectionTestResult{
		OK: false, Message: "indexer: fetch capabilities: connection refused", TestedAt: fixture.clock.Now(),
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers/"+itoa(created.ID)+"/test", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even for an unreachable indexer", recorder.Code)
	}
	if !contains(recorder.Body.String(), `"ok":false`) {
		t.Errorf("body = %s, want ok:false", recorder.Body.String())
	}
}

func TestTestIndexer_MissingAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.indexerTester.err = settings.ErrNotFound

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/indexers/999/test", ""))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

func TestTestIndexer_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/indexers/1/test", ""))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated request", recorder.Code)
	}
}

func TestTestDownloader_ReturnsTheTesterResult(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created, err := fixture.cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://sabnzbd.local:8080", Protocol: "usenet", Enabled: true,
	}, secrets.Secret(""), fixture.clock.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}
	fixture.downloaderTester.result = apiserver.ConnectionTestResult{
		OK: true, Message: "Connected. Version 5.1.3.", TestedAt: fixture.clock.Now(),
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders/"+itoa(created.ID)+"/test", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.downloaderTester.lastDownloaderID != created.ID {
		t.Errorf("lastDownloaderID = %d, want %d", fixture.downloaderTester.lastDownloaderID, created.ID)
	}
	if !contains(recorder.Body.String(), "5.1.3") {
		t.Errorf("body = %s, want the tester's own message", recorder.Body.String())
	}
}

func TestTestDownloader_MissingAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.downloaderTester.err = settings.ErrNotFound

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders/999/test", ""))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// "Test" before "Add": the unsaved settings reach the tester, and the client's folder comes back.
func TestTestDownloaderConfig_TestsWhatIsNotSavedYet(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.downloaderTester.result = apiserver.ConnectionTestResult{OK: true, Message: "Connected.", DownloadFolder: "/downloads/sabnzbd"}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders/test",
		`{"impl":"sabnzbd","base_url":"http://sab:8080","credentials":"key","options":{"category":"lidarr"}}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	if fixture.downloaderTester.lastConfig != "sabnzbd http://sab:8080 key lidarr" {
		t.Errorf("tested %q", fixture.downloaderTester.lastConfig)
	}
	if !strings.Contains(recorder.Body.String(), `"download_folder":"/downloads/sabnzbd"`) {
		t.Errorf("body = %s, want the download folder", recorder.Body)
	}
}

// A client's options are saved with it, and switching it off (no options sent) keeps them.
func TestDownloaderOptions_SavedAndKeptByAnUpdateWithoutThem(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloaders",
		`{"name":"SABnzbd","impl":"sabnzbd","base_url":"http://sab:8080","protocol":"usenet","options":{"category":"lidarr","remote_path":"/downloads","local_path":"/data/downloads"}}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	target := "/api/v1/downloaders/" + strconv.FormatInt(created.ID, 10)
	recorder = fixture.do(authedPatch(t, cookies, target, `{"name":"SABnzbd","impl":"sabnzbd","base_url":"http://sab:8080","protocol":"usenet","enabled":false}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update: %d %s", recorder.Code, recorder.Body)
	}
	stored, err := fixture.cache.GetDownloader(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Options(); got.Category != "lidarr" || got.LocalPathFor("/downloads/x") != "/data/downloads/x" {
		t.Errorf("options after update = %+v, want them kept", got)
	}
}
