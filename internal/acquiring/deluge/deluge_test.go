package deluge_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/deluge"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// daemon answers each method with its result; any call without the session cookie is not authenticated.
func daemon(results map[string]string) *clienttest.Server {
	server := &clienttest.Server{}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		var call struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal([]byte(request.Body), &call)
		if call.Method == "auth.login" {
			if string(call.Params[0]) != `"secret"` {
				return clienttest.Route{Body: `{"result":false,"error":null,"id":1}`}, true
			}
			return clienttest.Route{Body: `{"result":true,"error":null,"id":1}`,
				Header: http.Header{"Set-Cookie": {"_session_id=s1; Path=/json"}}}, true
		}
		if !strings.Contains(request.Header.Get("Cookie"), "_session_id=s1") {
			return clienttest.Route{Body: `{"result":null,"error":{"message":"Not authenticated","code":1},"id":1}`}, true
		}
		result, found := results[call.Method]
		if !found {
			return clienttest.Route{Body: `{"result":null,"error":{"message":"Unknown method","code":2},"id":1}`}, true
		}
		return clienttest.Route{Body: `{"result":` + result + `,"error":null,"id":1}`}, true
	}
	return server
}

func driver(t *testing.T, server *clienttest.Server, password string) download.Driver {
	t.Helper()
	return download.NewTorrent(deluge.Downloader{Client: deluge.NewClient(httpClient(t, server), "http://deluge:8112", password)})
}

func methods(server *clienttest.Server) []string {
	var names []string
	for _, seen := range server.Seen {
		var call struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(seen.Body), &call)
		names = append(names, call.Method)
	}
	return names
}

func TestDownloader_AddSignsInConnectsTheDaemonAndLabelsTheTorrent(t *testing.T) {
	server := daemon(map[string]string{
		"web.connected": "false", "web.get_hosts": `[["host1","127.0.0.1",58846,"Offline"]]`, "web.connect": "null",
		"core.add_torrent_file": `"abc"`, "label.get_labels": `["tv"]`, "label.add": "null", "label.set_torrent": "null",
	})
	ref, err := driver(t, server, "secret").Add(t.Context(), download.AddRequest{Name: "x", File: clienttest.TorrentFile("a"), Category: "Solarr-Music"})
	if err != nil || len(ref) != 40 {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	want := []string{"core.add_torrent_file", "auth.login", "web.connected", "web.get_hosts", "web.connect",
		"core.add_torrent_file", "label.get_labels", "label.add", "label.set_torrent"}
	if got := methods(server); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if body := server.Seen[len(server.Seen)-1].Body; !strings.Contains(body, `"solarr-music"`) {
		t.Errorf("label body = %s, want the label in lower case", body)
	}
}

func TestDownloader_ATorrentDelugeAlreadyHasIsRefused(t *testing.T) {
	server := daemon(map[string]string{"web.connected": "true", "core.add_torrent_file": "null"})
	if _, err := driver(t, server, "secret").Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a")}); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Errorf("Add = %v, want a refusal", err)
	}
}

func TestClient_AWrongPasswordSaysSo(t *testing.T) {
	_, err := driver(t, daemon(nil), "wrong").Probe(t.Context())
	if !errors.Is(err, deluge.ErrLoginRefused) {
		t.Errorf("Probe = %v, want ErrLoginRefused", err)
	}
}

func TestDownloader_DownloadsMapsStates(t *testing.T) {
	server := daemon(map[string]string{"web.connected": "true", "core.get_torrents_status": `{
	 "aaa":{"name":"Album","state":"Seeding","label":"music","total_size":10,"total_done":10,"save_path":"/downloads","is_finished":true,"ratio":2,"seeding_time":30},
	 "bbb":{"name":"Going","state":"Downloading","total_size":10,"total_done":6,"download_payload_rate":512},
	 "ccc":{"name":"Held","state":"Paused","total_size":10,"total_done":1},
	 "ddd":{"name":"Broken","state":"Error","message":"No space left"}}`})
	got, err := driver(t, server, "secret").Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	want := []download.Download{
		{ClientRef: "aaa", Name: "Album", Category: "music", State: download.StateCompleted, SizeBytes: 10, Path: "/downloads/Album", Ratio: 2, SeedingSeconds: 30},
		{ClientRef: "bbb", Name: "Going", State: download.StateDownloading, SizeBytes: 10, SizeLeftBytes: 4, SpeedBytesPerSec: 512},
		{ClientRef: "ccc", Name: "Held", State: download.StatePaused, SizeBytes: 10, SizeLeftBytes: 9},
		{ClientRef: "ddd", Name: "Broken", State: download.StateFailed, Error: "Deluge reports No space left"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Downloads =\n%+v\nwant\n%+v", got, want)
	}
}

// Deluge 2 pauses a list as core.pause_torrents; Deluge 1 only knows core.pause_torrent.
func TestDownloader_PauseFallsBackToDeluge1sMethod(t *testing.T) {
	server := daemon(map[string]string{"web.connected": "true", "core.pause_torrent": "null"})
	if err := driver(t, server, "secret").Pause(t.Context(), []string{"a", "b"}); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	got := methods(server)
	if got[len(got)-2] != "core.pause_torrents" || got[len(got)-1] != "core.pause_torrent" {
		t.Errorf("calls = %v", got)
	}
}

func TestDownloader_ProbeSaysWhenTheLabelPluginIsOff(t *testing.T) {
	server := daemon(map[string]string{
		"web.connected": "true", "daemon.info": `"2.1.1"`,
		"core.get_config": `{"download_location":"/incomplete","move_completed":true,"move_completed_path":"/downloads"}`,
	})
	got, err := driver(t, server, "secret").Probe(t.Context())
	if err != nil || got.Version != "Deluge 2.1.1" || got.DownloadFolder != "/downloads" || got.Categories != nil || len(got.Notes) != 1 {
		t.Errorf("Probe = %+v, %v", got, err)
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
