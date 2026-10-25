package nzbget_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/nzbget"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// rpc answers each method with its result.
func rpc(results map[string]string) *clienttest.Server {
	server := &clienttest.Server{}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		var call struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(request.Body), &call)
		result, found := results[call.Method]
		if !found {
			return clienttest.Route{Body: `{"error":{"name":"JSONRPCError","code":1,"message":"Invalid procedure"}}`}, true
		}
		return clienttest.Route{Body: `{"version":"1.1","result":` + result + `}`}, true
	}
	return server
}

func driver(t *testing.T, server *clienttest.Server) download.Driver {
	t.Helper()
	return download.NewUsenet(nzbget.Downloader{Client: nzbget.NewClient(httpClient(t, server), "http://nzbget:6789", "nzbget:tegbzn6789")})
}

func TestDownloader_AddUploadsTheNZBAndAnswersItsID(t *testing.T) {
	server := rpc(map[string]string{"append": "42"})
	ref, err := driver(t, server).Add(t.Context(), download.AddRequest{Name: "Heka-Zanuvode", File: clienttest.NZB(), Category: "solarr-music"})
	if err != nil || ref != "42" {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	seen := server.Seen[0]
	if seen.Path != "/jsonrpc" || seen.Header.Get("Authorization") == "" {
		t.Errorf("request = %+v, want the RPC path with basic auth", seen)
	}
	var call struct {
		Params []json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal([]byte(seen.Body), &call)
	if len(call.Params) != 10 || string(call.Params[0]) != `"Heka-Zanuvode.nzb"` || string(call.Params[2]) != `"solarr-music"` {
		t.Errorf("append params = %s", seen.Body)
	}

	refused := rpc(map[string]string{"append": "0"})
	if _, err := driver(t, refused).Add(t.Context(), download.AddRequest{File: clienttest.NZB()}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("NZBID 0: %v, want ErrRefused", err)
	}
	if _, err := driver(t, refused).Add(t.Context(), download.AddRequest{File: []byte("<html>error</html>")}); !errors.Is(err, download.ErrNotNZB) {
		t.Errorf("an HTML page: %v, want ErrNotNZB", err)
	}
}

func TestDownloader_DownloadsJoinsTheQueueAndTheHistory(t *testing.T) {
	server := rpc(map[string]string{
		"listgroups": `[
		 {"NZBID":1,"NZBName":"Going","Category":"music","Status":"DOWNLOADING","FileSizeLo":100,"FileSizeHi":1,"RemainingSizeLo":50},
		 {"NZBID":2,"NZBName":"Unpacking","Category":"music","Status":"UNPACKING","FileSizeLo":100}]`,
		"history": `[
		 {"NZBID":3,"Name":"Done","Category":"music","Status":"SUCCESS/UNPACK","DestDir":"/intermediate","FinalDir":"/downloads/Done","FileSizeLo":10,"Kind":"NZB"},
		 {"NZBID":4,"Name":"Broken","Category":"music","Status":"FAILURE/PAR","Kind":"NZB"},
		 {"NZBID":5,"Name":"feed","Kind":"URL","Status":"SUCCESS/ALL"}]`,
	})
	got, err := driver(t, server).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	want := []download.Download{
		{ClientRef: "1", Name: "Going", Category: "music", State: download.StateDownloading, SizeBytes: 1<<32 + 100, SizeLeftBytes: 50},
		{ClientRef: "2", Name: "Unpacking", Category: "music", State: download.StateProcessing, SizeBytes: 100},
		{ClientRef: "3", Name: "Done", Category: "music", State: download.StateCompleted, SizeBytes: 10, Path: "/downloads/Done"},
		{ClientRef: "4", Name: "Broken", Category: "music", State: download.StateFailed, Error: "NZBGet reports FAILURE/PAR"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Downloads =\n%+v\nwant\n%+v", got, want)
	}
}

// A job still queued is deleted with its files; a finished one only leaves the history.
func TestDownloader_RemoveUsesTheQueueOrTheHistoryCommand(t *testing.T) {
	server := rpc(map[string]string{"listgroups": `[{"NZBID":1}]`, "editqueue": "true"})
	driver := driver(t, server)
	for _, ref := range []string{"1", "9"} {
		if err := driver.Remove(t.Context(), ref, true); err != nil {
			t.Fatalf("Remove %s: %v", ref, err)
		}
	}
	var commands []string
	for _, body := range server.Bodies("/jsonrpc") {
		if strings.Contains(body, "editqueue") {
			var call struct {
				Params []json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal([]byte(body), &call)
			commands = append(commands, string(call.Params[0])+string(call.Params[2]))
		}
	}
	if want := []string{`"GroupFinalDelete"[1]`, `"HistoryFinalDelete"[9]`}; !reflect.DeepEqual(commands, want) {
		t.Errorf("commands = %v, want %v", commands, want)
	}
}

func TestDownloader_ProbeReadsTheFolderAndCategories(t *testing.T) {
	server := rpc(map[string]string{
		"version": `"24.3"`,
		"config":  `[{"Name":"DestDir","Value":"/downloads/completed"},{"Name":"Category2.Name","Value":"tv"},{"Name":"Category1.Name","Value":"music"}]`,
	})
	got, err := driver(t, server).Probe(t.Context())
	want := download.Probe{Version: "NZBGet 24.3", DownloadFolder: "/downloads/completed", Categories: []string{"music", "tv"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Probe = %+v, %v; want %+v", got, err, want)
	}
}

func TestDownloader_QueueStatusIsNZBGetsOwn(t *testing.T) {
	server := rpc(map[string]string{
		"status":     `{"RemainingSizeLo":300,"RemainingSizeHi":0,"DownloadRate":1000,"DownloadPaused":true}`,
		"listgroups": `[{"NZBID":1},{"NZBID":2}]`,
	})
	got, err := driver(t, server).QueueStatus(t.Context())
	if want := (download.QueueStatus{Paused: true, SpeedBytesPerSec: 1000, Items: 2, SizeLeftBytes: 300}); err != nil || got != want {
		t.Errorf("QueueStatus = %+v, %v; want %+v", got, err, want)
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
