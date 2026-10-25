package rtorrent_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/rtorrent"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func response(inner string) string {
	return `<?xml version="1.0"?><methodResponse><params><param><value>` + inner + `</value></param></params></methodResponse>`
}

// rpc answers each method with its value.
func rpc(values map[string]string) *clienttest.Server {
	server := &clienttest.Server{}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		start := strings.Index(request.Body, "<methodName>") + len("<methodName>")
		method := request.Body[start : start+strings.Index(request.Body[start:], "<")]
		inner, found := values[method]
		if !found {
			return clienttest.Route{Body: `<?xml version="1.0"?><methodResponse><fault><value><struct>` +
				`<member><name>faultCode</name><value><i4>-506</i4></value></member>` +
				`<member><name>faultString</name><value><string>Method '` + method + `' not defined</string></value></member>` +
				`</struct></value></fault></methodResponse>`}, true
		}
		return clienttest.Route{Body: response(inner)}, true
	}
	return server
}

func driver(t *testing.T, server *clienttest.Server) download.Driver {
	t.Helper()
	return download.NewTorrent(rtorrent.Downloader{Client: rtorrent.NewClient(httpClient(t, server), "http://rtorrent:8080", "", "")})
}

func TestDownloader_AddLoadsTheTorrentWithItsLabel(t *testing.T) {
	server := rpc(map[string]string{"load.raw_start": "<i4>0</i4>"})
	ref, err := driver(t, server).Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a"), Category: "solarr-music"})
	if err != nil || len(ref) != 40 {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	body := server.Seen[0].Body
	if server.Seen[0].Path != "/RPC2" || !strings.Contains(body, "<base64>") || !strings.Contains(body, "<string>d.custom1.set=solarr-music</string>") {
		t.Errorf("request %s %s", server.Seen[0].Path, body)
	}
	if _, err := driver(t, rpc(nil)).Add(t.Context(), download.AddRequest{File: []byte("magnet:?xt=urn:btih:" + strings.Repeat("a", 40))}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("a fault: %v, want ErrRefused", err)
	}
}

func row(columns ...string) string {
	return "<value><array><data>" + strings.Join(columns, "") + "</data></array></value>"
}

func text(value string) string   { return "<value><string>" + value + "</string></value>" }
func number(value string) string { return "<value><i8>" + value + "</i8></value>" }

func TestDownloader_DownloadsReadsTheMulticall(t *testing.T) {
	rows := "<array><data>" +
		row(text("AAAA"), text("Album"), text("/downloads/Album"), text("/downloads/Album"), text("music"), number("10"), number("0"),
			number("0"), number("1500"), number("1"), number("1"), number("1"), text("")) +
		row(text("BBBB"), text("Going"), text(""), text("/downloads"), text(""), number("10"), number("4"),
			number("99"), number("0"), number("1"), number("0"), number("1"), text("")) +
		row(text("CCCC"), text("track.flac"), text(""), text("/downloads"), text(""), number("10"), number("0"),
			number("0"), number("0"), number("0"), number("1"), number("0"), text("")) +
		"</data></array>"
	got, err := driver(t, rpc(map[string]string{"d.multicall2": rows})).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	want := []download.Download{
		{ClientRef: "aaaa", Name: "Album", Category: "music", State: download.StateCompleted, SizeBytes: 10, Path: "/downloads/Album", Ratio: 1.5},
		{ClientRef: "bbbb", Name: "Going", State: download.StateDownloading, SizeBytes: 10, SizeLeftBytes: 4, SpeedBytesPerSec: 99},
		{ClientRef: "cccc", Name: "track.flac", State: download.StateCompleted, SizeBytes: 10, Path: "/downloads/track.flac"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Downloads =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDownloader_RemoveMarksTheDataForErasureThenErases(t *testing.T) {
	server := rpc(map[string]string{"d.custom5.set": "<i4>0</i4>", "d.erase": "<i4>0</i4>"})
	if err := driver(t, server).Remove(t.Context(), "abcd", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(server.Seen) != 2 || !strings.Contains(server.Seen[0].Body, "d.custom5.set") || !strings.Contains(server.Seen[1].Body, "<string>ABCD</string>") {
		t.Errorf("requests = %+v", server.Seen)
	}
}

func TestDownloader_ProbeReadsTheVersion(t *testing.T) {
	server := rpc(map[string]string{"system.client_version": "<string>0.9.8</string>", "directory.default": "/downloads"})
	got, err := driver(t, server).Probe(t.Context())
	if err != nil || got.Version != "rTorrent 0.9.8" || got.DownloadFolder != "/downloads" {
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
