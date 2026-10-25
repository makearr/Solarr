package aria2_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/aria2"
	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// rpc answers each method with its result, refusing a call whose first parameter is not the secret.
func rpc(results map[string]string) *clienttest.Server {
	server := &clienttest.Server{}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		var call struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal([]byte(request.Body), &call)
		if len(call.Params) == 0 || string(call.Params[0]) != `"token:s3cret"` {
			return clienttest.Route{Body: `{"id":"solarr","jsonrpc":"2.0","error":{"code":1,"message":"Unauthorized"}}`}, true
		}
		result, found := results[call.Method]
		if !found {
			return clienttest.Route{Body: `{"id":"solarr","jsonrpc":"2.0","error":{"code":1,"message":"No such method"}}`}, true
		}
		return clienttest.Route{Body: `{"id":"solarr","jsonrpc":"2.0","result":` + result + `}`}, true
	}
	return server
}

func driver(t *testing.T, server *clienttest.Server, secret string) download.Driver {
	t.Helper()
	return download.NewTorrent(aria2.Downloader{Client: aria2.NewClient(httpClient(t, server), "http://aria2:6800", "", secret)})
}

func TestDownloader_AddSendsTheSecretFirstAndAnswersTheInfoHash(t *testing.T) {
	server := rpc(map[string]string{"aria2.addTorrent": `"2089b05ecca3d829"`})
	ref, err := driver(t, server, "s3cret").Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a")})
	if err != nil || len(ref) != 40 || server.Seen[0].Path != "/rpc" {
		t.Fatalf("Add = %q, %v at %s", ref, err, server.Seen[0].Path)
	}
	if _, err := driver(t, server, "wrong").Add(t.Context(), download.AddRequest{File: clienttest.TorrentFile("a")}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("a wrong secret: %v", err)
	}
}

func status(gid, state, hash, name, total, done string) string {
	return `{"gid":"` + gid + `","status":"` + state + `","infoHash":"` + hash + `","totalLength":"` + total +
		`","completedLength":"` + done + `","downloadSpeed":"5","dir":"/downloads","bittorrent":{"info":{"name":"` + name + `"}}}`
}

func TestDownloader_DownloadsJoinsEveryListByInfoHash(t *testing.T) {
	server := rpc(map[string]string{
		"aria2.tellActive":  `[` + status("1", "active", "AAAA", "Album", "10", "10") + `,` + status("2", "active", "bbbb", "Going", "10", "4") + `]`,
		"aria2.tellWaiting": `[` + status("3", "paused", "cccc", "Held", "10", "1") + `]`,
		"aria2.tellStopped": `[` + status("4", "complete", "dddd", "", "0", "0") + `,` + status("5", "active", "dddd", "Magnet", "10", "2") + `]`,
	})
	got, err := driver(t, server, "s3cret").Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	var summary []string
	for _, one := range got {
		summary = append(summary, one.ClientRef+" "+one.Name+" "+string(one.State)+" "+one.Path)
	}
	want := []string{"aaaa Album completed /downloads/Album", "bbbb Going downloading ", "cccc Held paused ", "dddd Magnet downloading "}
	if !reflect.DeepEqual(summary, want) {
		t.Errorf("downloads = %q, want %q", summary, want)
	}
}

func TestDownloader_RemoveFindsTheGidByHash(t *testing.T) {
	server := rpc(map[string]string{
		"aria2.tellActive": `[` + status("7", "active", "aaaa", "Album", "10", "4") + `]`, "aria2.tellWaiting": `[]`, "aria2.tellStopped": `[]`,
		"aria2.forceRemove": `"7"`, "aria2.removeDownloadResult": `"OK"`,
	})
	if err := driver(t, server, "s3cret").Remove(t.Context(), "AAAA", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	last := server.Seen[len(server.Seen)-1].Body
	if !strings.Contains(last, "removeDownloadResult") || !strings.Contains(last, `"7"`) {
		t.Errorf("last call = %s", last)
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
