package utorrent_test

import (
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/utorrent"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// webUI hands out token t1 with cookie g1, refuses any other token with 400, and answers each action.
func webUI(actions map[string]string) *clienttest.Server {
	server := &clienttest.Server{}
	tokens := []string{"t0", "t1"}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		if request.Path == "/gui/token.html" {
			token := tokens[0]
			tokens = tokens[1:]
			return clienttest.Route{Body: "<html><div id='token' style='display:none;'>" + token + "</div></html>",
				Header: http.Header{"Set-Cookie": {"GUID=g1"}}}, true
		}
		query, _ := url.ParseQuery(request.Query)
		if query.Get("token") != "t1" || !strings.Contains(request.Header.Get("Cookie"), "GUID=g1") {
			return clienttest.Route{Status: http.StatusBadRequest}, true
		}
		key := query.Get("action")
		if query.Get("list") == "1" {
			key = "list"
		}
		return clienttest.Route{Body: actions[key]}, true
	}
	return server
}

func driver(t *testing.T, server *clienttest.Server) download.Driver {
	t.Helper()
	return download.NewTorrent(utorrent.Downloader{Client: utorrent.NewClient(httpClient(t, server), "http://utorrent:8080", "admin:pw")})
}

// The first token is stale: the call is refused once, a fresh token fetched, and the call made again.
func TestDownloader_AddRefreshesAStaleTokenAndLabelsTheTorrent(t *testing.T) {
	server := webUI(map[string]string{"add-file": `{"build":46000}`, "setprops": `{"build":46000}`})
	ref, err := driver(t, server).Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a"), Category: "solarr-music"})
	if err != nil || len(ref) != 40 {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	last, _ := url.ParseQuery(server.Seen[len(server.Seen)-1].Query)
	if last.Get("action") != "setprops" || last.Get("hash") != strings.ToUpper(ref) || last.Get("v") != "solarr-music" {
		t.Errorf("label request = %v", last)
	}
	if !strings.Contains(server.Bodies("/gui/")[1], `name="torrent_file"`) {
		t.Errorf("add body = %q", server.Bodies("/gui/")[1])
	}
}

func TestDownloader_DownloadsReadsTheColumns(t *testing.T) {
	row := func(hash string, status int, name string, progress, remaining int, savePath string) string {
		return `["` + hash + `",` + strconv.Itoa(status) + `,"` + name + `",100,` + strconv.Itoa(progress) + `,0,0,1500,0,50,0,"music",0,0,0,0,0,1,` +
			strconv.Itoa(remaining) + `,"","","Status",0,0,0,"","` + savePath + `"]`
	}
	list := `{"build":46000,"label":[["music",2]],"torrents":[` +
		row("AAAA", 1|8|128, "Album", 1000, 0, "/downloads/Album") + "," +
		row("BBBB", 1|8|128, "track.flac", 1000, 0, "/downloads") + "," +
		row("CCCC", 1|8|128, "Going", 500, 50, "/downloads/Going") + "," +
		row("DDDD", 1|32|128, "Held", 500, 50, "/downloads/Held") + "," +
		row("EEEE", 16|128, "Broken", 500, 50, "/downloads/Broken") + `]}`
	got, err := driver(t, webUI(map[string]string{"list": list})).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	var summary []string
	for _, one := range got {
		summary = append(summary, one.ClientRef+" "+string(one.State)+" "+one.Path)
	}
	want := []string{"aaaa completed /downloads/Album", "bbbb completed /downloads/track.flac", "cccc downloading ",
		"dddd paused ", "eeee failed "}
	if !reflect.DeepEqual(summary, want) {
		t.Errorf("downloads = %q, want %q", summary, want)
	}
	if got[0].Ratio != 1.5 || got[0].Category != "music" || got[2].SpeedBytesPerSec != 50 {
		t.Errorf("first = %+v", got[0])
	}
}

func TestDownloader_ProbeReadsTheBuildLabelsAndFolder(t *testing.T) {
	server := webUI(map[string]string{
		"list":        `{"build":46000,"label":[["music",2]],"torrents":[]}`,
		"getsettings": `{"settings":[["dir_active_download",2,"/incomplete"],["dir_completed_download_flag",1,"true"],["dir_completed_download",2,"/downloads"]]}`,
	})
	got, err := driver(t, server).Probe(t.Context())
	want := download.Probe{Version: "µTorrent build 46000", Categories: []string{"music"}, CreatesCategories: true, DownloadFolder: "/downloads"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Probe = %+v, %v; want %+v", got, err, want)
	}
}

func TestDownloader_RemoveWithFilesUsesRemoveData(t *testing.T) {
	server := webUI(map[string]string{"removedata": `{}`})
	if err := driver(t, server).Remove(t.Context(), "abcd", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	last, _ := url.ParseQuery(server.Seen[len(server.Seen)-1].Query)
	if last.Get("action") != "removedata" || last.Get("hash") != "ABCD" {
		t.Errorf("request = %v", last)
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
