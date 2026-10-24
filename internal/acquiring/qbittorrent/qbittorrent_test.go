package qbittorrent_test

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/qbittorrent"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// route answers by path, recording every request it saw.
type route struct {
	status int
	body   string
	header http.Header
}

type fakeServer struct {
	routes    map[string]route
	requested []*http.Request
	bodies    []string
}

func (f *fakeServer) RoundTrip(request *http.Request) (*http.Response, error) {
	f.requested = append(f.requested, request)
	body := ""
	if request.Body != nil {
		raw, _ := io.ReadAll(request.Body)
		body = string(raw)
	}
	f.bodies = append(f.bodies, body)
	answer, found := f.routes[request.URL.Path]
	if !found {
		answer = route{status: http.StatusNotFound}
	}
	if answer.status == 0 {
		answer.status = http.StatusOK
	}
	header := answer.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: answer.status, Header: header, Body: io.NopCloser(strings.NewReader(answer.body)), Request: request}, nil
}

func newClient(t *testing.T, server *fakeServer, credentials string) qbittorrent.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "qbittorrent-test", UserAgent: "test", Transport: server,
	})
	return qbittorrent.NewClient(client, "http://qbit.test:8086/", credentials)
}

const torrentInfo = "d6:lengthi5e4:name1:a12:piece lengthi16384e6:pieces20:01234567890123456789e"

func torrentFile() []byte { return []byte("d8:announce3:url4:info" + torrentInfo + "7:comment1:xe") }

func TestDownloader_AddSendsTheTorrentUnderTheCategoryAndAnswersTheInfoHash(t *testing.T) {
	server := &fakeServer{routes: map[string]route{"/api/v2/torrents/add": {body: "Ok."}}}
	downloader := qbittorrent.Downloader{Client: newClient(t, server, "")}
	ref, err := downloader.Add(t.Context(), download.AddRequest{Name: "x", File: torrentFile(), Category: "solarr-music"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(ref) != 40 {
		t.Errorf("ref = %q, want an info hash", ref)
	}
	if body := server.bodies[0]; !strings.Contains(body, "solarr-music") || !strings.Contains(body, `name="torrents"`) ||
		!strings.Contains(body, "Subfolder") {
		t.Errorf("add body missing the category or the file: %q", body)
	}
	if got := server.requested[0].Header.Get("Referer"); got != "http://qbit.test:8086" {
		t.Errorf("Referer = %q", got)
	}

	for _, body := range []string{`{"success_count":1,"failure_count":0,"pending_count":0}`, `{"success_count":0,"failure_count":0,"pending_count":1}`} {
		server.routes["/api/v2/torrents/add"] = route{body: body}
		if _, err := downloader.Add(t.Context(), download.AddRequest{File: torrentFile()}); err != nil {
			t.Errorf("v5.2 answer %s: %v, want accepted", body, err)
		}
	}
	server.routes["/api/v2/torrents/add"] = route{body: `{"success_count":0,"failure_count":1,"pending_count":0}`}
	if _, err := downloader.Add(t.Context(), download.AddRequest{File: torrentFile()}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("a failure count: %v, want ErrRefused", err)
	}
	server.routes["/api/v2/torrents/add"] = route{body: "Fails."}
	if _, err := downloader.Add(t.Context(), download.AddRequest{File: torrentFile()}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("Fails.: %v, want ErrRefused", err)
	}
}

func TestDownloader_DownloadsMapsStatesAndOnlyACompletedOneHasAPath(t *testing.T) {
	server := &fakeServer{routes: map[string]route{"/api/v2/torrents/info": {body: `[
	 {"hash":"a","name":"seeding","category":"c","state":"stalledUP","size":10,"amount_left":0,"content_path":"/downloads/x","ratio":1.5,"seeding_time":3600},
	 {"hash":"b","name":"going","category":"c","state":"downloading","size":10,"amount_left":4,"content_path":"/downloads/y"},
	 {"hash":"c","name":"stopped","category":"c","state":"stoppedDL","size":10,"amount_left":9},
	 {"hash":"d","name":"bad","category":"c","state":"missingFiles","size":10,"amount_left":9},
	 {"hash":"e","name":"seeding to peers","category":"c","state":"uploading","size":10,"amount_left":0,"content_path":"/downloads/z"},
	 {"hash":"f","name":"rechecking","category":"c","state":"checkingUP","size":10,"amount_left":0,"content_path":"/downloads/w"}]`}}}
	got, err := qbittorrent.Downloader{Client: newClient(t, server, "")}.Downloads(t.Context(), "c")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	var states []download.State
	for _, one := range got {
		states = append(states, one.State)
	}
	want := []download.State{download.StateCompleted, download.StateDownloading, download.StatePaused, download.StateFailed,
		download.StateCompleted, download.StateProcessing}
	if len(states) != len(want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("states = %v, want %v", states, want)
		}
	}
	if got[0].Path != "/downloads/x" || got[1].Path != "" || got[3].Error == "" {
		t.Errorf("paths and errors wrong: %+v", got)
	}
	if got[0].Ratio != 1.5 || got[0].SeedingSeconds != 3600 {
		t.Errorf("seeding figures = %v and %d s, want 1.5 and 3600", got[0].Ratio, got[0].SeedingSeconds)
	}
	if server.requested[0].URL.RawQuery != "category=c" {
		t.Errorf("query = %q", server.requested[0].URL.RawQuery)
	}
}

func TestClient_LogsInWithCredentialsAndSendsTheSessionCookie(t *testing.T) {
	header := make(http.Header)
	header.Add("Set-Cookie", "QBT_SID_8086=abc123; HttpOnly")
	server := &fakeServer{routes: map[string]route{
		"/api/v2/auth/login":  {body: "Ok.", header: header},
		"/api/v2/app/version": {body: "v5.2.3"},
	}}
	version, err := newClient(t, server, "me:secret").Version(t.Context())
	if err != nil || version != "v5.2.3" {
		t.Fatalf("Version = %q, %v", version, err)
	}
	if cookie, _ := server.requested[1].Cookie("QBT_SID_8086"); cookie == nil || cookie.Value != "abc123" {
		t.Errorf("the version call carried no session cookie")
	}
	if !strings.Contains(server.bodies[0], "username=me") || !strings.Contains(server.bodies[0], "password=secret") {
		t.Errorf("login body = %q", server.bodies[0])
	}

	server.routes["/api/v2/auth/login"] = route{body: "Fails."}
	if _, err := newClient(t, server, "me:wrong").Version(t.Context()); !errors.Is(err, qbittorrent.ErrLoginRefused) {
		t.Errorf("wrong password: %v, want ErrLoginRefused", err)
	}
}

func TestClient_AForbiddenAnswerWithoutCredentialsSaysSo(t *testing.T) {
	server := &fakeServer{routes: map[string]route{"/api/v2/app/version": {status: http.StatusForbidden}}}
	if _, err := newClient(t, server, "").Version(t.Context()); !errors.Is(err, qbittorrent.ErrForbidden) {
		t.Errorf("403: %v, want ErrForbidden", err)
	}
}

func TestDownloader_PauseFallsBackToTheOlderEndpointName(t *testing.T) {
	server := &fakeServer{routes: map[string]route{"/api/v2/torrents/pause": {}}}
	if err := (qbittorrent.Downloader{Client: newClient(t, server, "")}).Pause(t.Context(), []string{"a", "b"}); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if len(server.requested) != 2 || server.requested[1].URL.Path != "/api/v2/torrents/pause" || server.bodies[1] != "hashes=a%7Cb" {
		t.Errorf("requests = %v bodies %v, want stop then pause with both hashes", server.requested, server.bodies)
	}
}

// A torrent's finished, wanted files are named below its own folder, where qBittorrent writes unfinished torrents; one
// whose files are loose in the client's folder has no folder of its own and answers none.
func TestDownloader_FinishedFilesAreTheCompleteOnesBelowTheTorrentsFolder(t *testing.T) {
	server := &fakeServer{routes: map[string]route{
		"/api/v2/torrents/info": {body: `[{"hash":"abc","save_path":"/downloads/arr","download_path":"/downloads/incomplete"}]`},
		"/api/v2/torrents/files": {body: `[
			{"name":"Heka - Zanuvode/01 - Kelo.flac","progress":1,"priority":1},
			{"name":"Heka - Zanuvode/02 - Mira.flac","progress":0.4,"priority":1},
			{"name":"Heka - Zanuvode/CD2/03 - Vosa.flac","progress":1,"priority":1},
			{"name":"Heka - Zanuvode/cover.jpg","progress":1,"priority":0}]`},
	}}
	downloader := qbittorrent.Downloader{Client: newClient(t, server, "")}
	folder, files, err := downloader.FinishedFiles(t.Context(), "abc")
	if err != nil || folder != "/downloads/incomplete/Heka - Zanuvode" || !slices.Equal(files, []string{"01 - Kelo.flac", "CD2/03 - Vosa.flac"}) {
		t.Errorf("FinishedFiles = %q %q, %v; want the two finished tracks below the torrent's folder", folder, files, err)
	}

	server.routes["/api/v2/torrents/files"] = route{body: `[{"name":"one.mkv","progress":1,"priority":1},{"name":"two.mkv","progress":0,"priority":1}]`}
	if folder, files, err := downloader.FinishedFiles(t.Context(), "abc"); err != nil || folder != "" || files != nil {
		t.Errorf("loose files = %q %q, %v; want none", folder, files, err)
	}
}
