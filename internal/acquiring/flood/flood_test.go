package flood_test

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/flood"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// api signs in me:secret with cookie j1, refuses any call without it with 401, and answers by path.
func api(routes map[string]string) *clienttest.Server {
	server := &clienttest.Server{}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		if request.Path == "/api/auth/authenticate" {
			if !strings.Contains(request.Body, `"password":"secret"`) {
				return clienttest.Route{Status: http.StatusUnauthorized}, true
			}
			return clienttest.Route{Body: `{"success":true}`, Header: http.Header{"Set-Cookie": {"jwt=j1; Path=/"}}}, true
		}
		if !strings.Contains(request.Header.Get("Cookie"), "jwt=j1") {
			return clienttest.Route{Status: http.StatusUnauthorized}, true
		}
		body, found := routes[request.Path]
		if !found {
			return clienttest.Route{Status: http.StatusNotFound}, true
		}
		return clienttest.Route{Body: body}, true
	}
	return server
}

func driver(t *testing.T, server *clienttest.Server, credentials string) download.Driver {
	t.Helper()
	return download.NewTorrent(flood.Downloader{Client: flood.NewClient(httpClient(t, server), "http://flood:3000", credentials)})
}

func TestDownloader_AddSignsInAndTagsTheTorrent(t *testing.T) {
	server := api(map[string]string{"/api/torrents/add-files": ""})
	ref, err := driver(t, server, "me:secret").Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a"), Category: "solarr-music"})
	if err != nil || len(ref) != 40 {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	if body := server.Bodies("/api/torrents/add-files")[0]; !strings.Contains(body, `"tags":["solarr-music"]`) || !strings.Contains(body, `"start":true`) {
		t.Errorf("add body = %s", body)
	}
	if _, err := driver(t, api(nil), "me:wrong").Probe(t.Context()); !errors.Is(err, flood.ErrLoginRefused) {
		t.Errorf("a wrong password: %v, want ErrLoginRefused", err)
	}
}

func TestDownloader_DownloadsReadsTheStatusList(t *testing.T) {
	server := api(map[string]string{"/api/torrents": `{"id":1,"torrents":{
	 "AAAA":{"name":"Album","status":["seeding","complete"],"sizeBytes":10,"bytesDone":10,"directory":"/downloads/Album","ratio":2,"tags":["music"]},
	 "BBBB":{"name":"Going","status":["downloading","active"],"sizeBytes":10,"bytesDone":4,"directory":"/downloads/Going","downRate":7},
	 "CCCC":{"name":"Held","status":["stopped","inactive"],"sizeBytes":10,"bytesDone":1,"directory":"/downloads/Held"},
	 "DDDD":{"name":"Broken","status":["error"],"message":"Tracker gone"}}}`})
	got, err := driver(t, server, "me:secret").Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	want := []download.Download{
		{ClientRef: "aaaa", Name: "Album", Category: "music", State: download.StateCompleted, SizeBytes: 10, Path: "/downloads/Album", Ratio: 2},
		{ClientRef: "bbbb", Name: "Going", State: download.StateDownloading, SizeBytes: 10, SizeLeftBytes: 6, SpeedBytesPerSec: 7},
		{ClientRef: "cccc", Name: "Held", State: download.StatePaused, SizeBytes: 10, SizeLeftBytes: 9},
		{ClientRef: "dddd", Name: "Broken", State: download.StateFailed, Error: "Flood reports Tracker gone"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Downloads =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDownloader_ProbeFailsWhenFloodHasNoClient(t *testing.T) {
	server := api(map[string]string{"/api/client/connection-test": `{"isConnected":false}`})
	if _, err := driver(t, server, "me:secret").Probe(t.Context()); !errors.Is(err, flood.ErrNotConnected) {
		t.Errorf("Probe = %v, want ErrNotConnected", err)
	}
	server = api(map[string]string{"/api/client/connection-test": `{"isConnected":true}`, "/api/client/settings": `{"directoryDefault":"/downloads"}`})
	if got, err := driver(t, server, "me:secret").Probe(t.Context()); err != nil || got.DownloadFolder != "/downloads" {
		t.Errorf("Probe = %+v, %v", got, err)
	}
}

func TestDownloader_RemoveSendsTheUpperCaseHash(t *testing.T) {
	server := api(map[string]string{"/api/torrents/delete": ""})
	if err := driver(t, server, "me:secret").Remove(t.Context(), "abcd", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if body := server.Bodies("/api/torrents/delete")[0]; body != `{"hashes":["ABCD"],"deleteData":true}` {
		t.Errorf("body = %s", body)
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
