package transmission_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/transmission"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// rpc answers each method with its own arguments, after first refusing a call without the session token.
func rpc(answers map[string]string) *clienttest.Server {
	server := &clienttest.Server{}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		if request.Header.Get("X-Transmission-Session-Id") != "token-1" {
			return clienttest.Route{Status: http.StatusConflict, Header: http.Header{"X-Transmission-Session-Id": {"token-1"}}}, true
		}
		var call struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(request.Body), &call)
		arguments, found := answers[call.Method]
		if !found {
			return clienttest.Route{Body: `{"result":"method name not recognized"}`}, true
		}
		return clienttest.Route{Body: `{"result":"success","arguments":` + arguments + `}`}, true
	}
	return server
}

func downloader(t *testing.T, server *clienttest.Server, vuze bool) download.Driver {
	t.Helper()
	client := transmission.NewClient(httpClient(t, server), "http://transmission:9091", "", "me:secret")
	return download.NewTorrent(transmission.Downloader{Client: client, Vuze: vuze})
}

func TestDownloader_AddFetchesTheSessionTokenAndFilesTheCategoryAsAFolder(t *testing.T) {
	server := rpc(map[string]string{
		"session-get": `{"version":"4.0.5","rpc-version":17,"download-dir":"/downloads"}`,
		"torrent-add": `{"torrent-added":{"hashString":"x"}}`,
	})
	ref, err := downloader(t, server, false).Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a"), Category: "solarr-music"})
	if err != nil || len(ref) != 40 {
		t.Fatalf("Add = %q, %v; want the info hash", ref, err)
	}
	if server.Seen[0].Path != "/transmission/rpc" || server.Seen[0].Header.Get("Authorization") == "" {
		t.Errorf("first request = %+v, want the RPC path with basic auth", server.Seen[0])
	}
	added := server.Seen[len(server.Seen)-1].Body
	if !strings.Contains(added, `"download-dir":"/downloads/solarr-music"`) || !strings.Contains(added, `"metainfo":"`) {
		t.Errorf("torrent-add body = %s, want the file below the category's folder", added)
	}
}

func TestDownloader_AnErrorAddingIsARefusal(t *testing.T) {
	server := rpc(map[string]string{"torrent-add": `{}`})
	server.Answer = func(clienttest.Request) (clienttest.Route, bool) {
		return clienttest.Route{Body: `{"result":"invalid or corrupt torrent file"}`}, true
	}
	if _, err := downloader(t, server, false).Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a")}); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Errorf("Add = %v, want a refusal", err)
	}
}

func TestDownloader_DownloadsMapsStatusesAndPaths(t *testing.T) {
	torrents := `{"torrents":[
	 {"hashString":"AAA","name":"Album","downloadDir":"/downloads/music","status":6,"totalSize":10,"leftUntilDone":0,"uploadRatio":1.5,"secondsSeeding":60},
	 {"hashString":"bbb","name":"Going","downloadDir":"/downloads","status":4,"totalSize":10,"leftUntilDone":4,"rateDownload":100},
	 {"hashString":"ccc","name":"Stopped","downloadDir":"/downloads","status":0,"totalSize":10,"leftUntilDone":9},
	 {"hashString":"ddd","name":"Broken","downloadDir":"/downloads","status":0,"error":3,"errorString":"No data found"},
	 {"hashString":"eee","name":"Done","downloadDir":"/downloads","status":0,"isFinished":true,"totalSize":10},
	 {"hashString":"fff","name":"Waiting","downloadDir":"/downloads","status":3,"totalSize":10,"leftUntilDone":10}]}`
	got, err := downloader(t, rpc(map[string]string{"torrent-get": torrents}), false).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	want := []download.Download{
		{ClientRef: "aaa", Name: "Album", State: download.StateCompleted, SizeBytes: 10, Path: "/downloads/music/Album", Ratio: 1.5, SeedingSeconds: 60},
		{ClientRef: "bbb", Name: "Going", State: download.StateDownloading, SizeBytes: 10, SizeLeftBytes: 4, SpeedBytesPerSec: 100},
		{ClientRef: "ccc", Name: "Stopped", State: download.StatePaused, SizeBytes: 10, SizeLeftBytes: 9},
		{ClientRef: "ddd", Name: "Broken", State: download.StateFailed, Error: "No data found"},
		{ClientRef: "eee", Name: "Done", State: download.StateCompleted, SizeBytes: 10, Path: "/downloads/Done"},
		{ClientRef: "fff", Name: "Waiting", State: download.StateQueued, SizeBytes: 10, SizeLeftBytes: 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Downloads =\n%+v\nwant\n%+v", got, want)
	}
}

// Vuze names a multi-file torrent's own folder as its download folder; a single file lies in the folder it names.
func TestVuze_PathsFollowItsOwnFolderLayout(t *testing.T) {
	torrents := `{"torrents":[
	 {"hashString":"a","name":"Album","downloadDir":"/downloads/Album","status":6,"fileCount":12},
	 {"hashString":"b","name":"track.flac","downloadDir":"/downloads","status":6,"fileCount":1}]}`
	got, err := downloader(t, rpc(map[string]string{"torrent-get": torrents}), true).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	if got[0].Path != "/downloads/Album" || got[1].Path != "/downloads/track.flac" {
		t.Errorf("paths = %q and %q", got[0].Path, got[1].Path)
	}
}

func TestDownloader_QueueStatusCountsWhatIsStillDownloading(t *testing.T) {
	server := rpc(map[string]string{
		"torrent-get":   `{"torrents":[{"hashString":"a","status":4,"leftUntilDone":4},{"hashString":"b","status":6,"leftUntilDone":0}]}`,
		"session-stats": `{"downloadSpeed":2048}`,
	})
	got, err := downloader(t, server, false).QueueStatus(t.Context())
	if err != nil || got != (download.QueueStatus{Items: 1, SizeLeftBytes: 4, SpeedBytesPerSec: 2048}) {
		t.Errorf("QueueStatus = %+v, %v", got, err)
	}
}

func TestDownloader_ProbeReadsTheVersionAndDownloadFolder(t *testing.T) {
	server := rpc(map[string]string{"session-get": `{"version":"4.0.5 (a6fe2a64aa)","rpc-version":17,"download-dir":"/downloads"}`})
	got, err := downloader(t, server, false).Probe(t.Context())
	if err != nil || got.Version != "Transmission 4.0.5 (a6fe2a64aa)" || got.DownloadFolder != "/downloads" {
		t.Errorf("Probe = %+v, %v", got, err)
	}
}

func TestDownloader_RemoveAndPauseActOnTheNamedTorrentsOnly(t *testing.T) {
	server := rpc(map[string]string{"torrent-remove": `{}`, "torrent-stop": `{}`})
	driver := downloader(t, server, false)
	if err := driver.Remove(t.Context(), "abc", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := driver.Pause(t.Context(), []string{"abc", "def"}); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	bodies := server.Bodies("/transmission/rpc")
	if !strings.Contains(bodies[1], `"ids":["abc"],"delete-local-data":true`) || !strings.Contains(bodies[2], `"ids":["abc","def"]`) {
		t.Errorf("bodies = %q", bodies)
	}
}

// httpClient is an httpx.Client over server, with limits no test reaches.
func httpClient(t *testing.T, server *clienttest.Server) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "download-client-test", UserAgent: "test", Transport: server,
	})
}
