package specclient_test

import (
	"bytes"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
	"github.com/makearr/Solarr/internal/acquiring/specclient"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Recorded answers in SABnzbd's shape; every name is invented.
var sabAnswers = map[string]string{
	"version":    `{"version":"4.3.1"}`,
	"get_config": `{"config":{"misc":{"complete_dir":"/downloads/complete"}}}`,
	"get_cats":   `{"categories":["*","music","books"]}`,
	"queue": `{"queue":{"paused":true,"mb":"900.5","mbleft":"700.25","kbpersec":"4800.50","noofslots_total":4,"noofslots":4,"slots":[
	  {"nzo_id":"nzo_a1","filename":"Quiet.Harbour-Night.Songs-2021-FLAC","cat":"music","mb":"734.4","mbleft":"200.1","percentage":"72","status":"Downloading"},
	  {"nzo_id":"nzo_b2","filename":"Other.Release","cat":"music","mb":"500.0","mbleft":"500.0","percentage":"0","status":"Queued"},
	  {"nzo_id":"nzo_c3","filename":"Trying to fetch NZB from http://x/?apikey=secret","cat":"music","mb":"0","mbleft":"0","percentage":"0","status":"Grabbing"},
	  {"nzo_id":"nzo_d4","filename":"Elsewhere","cat":"books","mb":"1.5","mbleft":"1.5","percentage":"0","status":"Paused"}]}}`,
	"history": `{"history":{"slots":[
	  {"nzo_id":"nzo_e5","name":"Done.Album-FLAC","category":"music","status":"Completed","storage":"/downloads/complete/Done.Album-FLAC","fail_message":"","bytes":300000000,"completed":1790000000},
	  {"nzo_id":"nzo_f6","name":"Broken","category":"music","status":"Failed","storage":"/downloads/complete/Broken","fail_message":"Repair failed, not enough repair blocks","bytes":12,"completed":1790000100},
	  {"nzo_id":"nzo_g7","name":"Unpacking.Now","category":"music","status":"Extracting","storage":"","fail_message":"","bytes":99,"completed":0}]}}`,
	"addfile": `{"status":true,"nzo_ids":["nzo_h8"]}`,
}

// sabServer is clienttest.Server answering SABnzbd's modes; the same server serves the compiled client and the definition.
func sabServer(answers map[string]string) *clienttest.Server {
	return &clienttest.Server{Answer: func(request clienttest.Request) (clienttest.Route, bool) {
		values, _ := url.ParseQuery(request.Query)
		mode := values.Get("mode")
		if values.Get("name") != "" && (mode == "queue" || mode == "history") {
			return clienttest.Route{Body: `{"status":true}`}, true
		}
		body, found := answers[mode]
		return clienttest.Route{Body: body}, found
	}}
}

func testClient(t *testing.T, server *clienttest.Server) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{Provider: "t", UserAgent: "t", Transport: server})
}

func loadPort(t *testing.T) specclient.Definition {
	t.Helper()
	data, err := os.ReadFile("testdata/sabnzbd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	def, err := specclient.Parse("sabnzbd-port", "SABnzbd (definition)", data)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// calls are what a server saw, minus the uploaded body's random boundary, as "METHOD path ?sorted query".
func calls(server *clienttest.Server) []string {
	var seen []string
	for _, request := range server.Seen {
		values, _ := url.ParseQuery(request.Query)
		var pairs []string
		for name, list := range values {
			pairs = append(pairs, name+"="+list[0])
		}
		sort.Strings(pairs)
		seen = append(seen, request.Method+" "+request.Path+" ?"+strings.Join(pairs, "&"))
	}
	return seen
}

type pair struct {
	compiled download.Driver
	port     download.Driver
	cServer  *clienttest.Server
	pServer  *clienttest.Server
}

func newPair(t *testing.T, answers map[string]string) pair {
	t.Helper()
	cServer, pServer := sabServer(answers), sabServer(answers)
	const base = "http://sabnzbd.local:8080"
	return pair{
		compiled: download.NewUsenet(sabnzbd.Downloader{Client: sabnzbd.NewClient(testClient(t, cServer), base, "the-key")}),
		port:     download.NewUsenet(specclient.New(loadPort(t), testClient(t, pServer), clock.NewFakeAtEpoch(), base, "the-key")),
		cServer:  cServer, pServer: pServer,
	}
}

func (p pair) same(t *testing.T, operation string, compiled, port any) {
	t.Helper()
	if !reflect.DeepEqual(compiled, port) {
		t.Errorf("%s: definition = %+v, compiled = %+v", operation, port, compiled)
	}
	if got, want := calls(p.pServer), calls(p.cServer); !reflect.DeepEqual(got, want) {
		t.Errorf("%s: requests differ\n definition %q\n compiled   %q", operation, got, want)
	}
	p.cServer.Seen, p.pServer.Seen = nil, nil
}

func TestThePortMakesTheCompiledClientsRequestsAndReadsTheSameAnswers(t *testing.T) {
	p := newPair(t, sabAnswers)
	ctx := t.Context()
	// The probe's version text and folder are equal; the compiled one also lists categories.
	cProbe, cErr := p.compiled.Probe(ctx)
	pProbe, pErr := p.port.Probe(ctx)
	if cErr != nil || pErr != nil {
		t.Fatalf("probe: %v, %v", cErr, pErr)
	}
	p.same(t, "probe", cProbe, pProbe)
	if cProbe.Version != "SABnzbd 4.3.1" || cProbe.DownloadFolder != "/downloads/complete" || len(cProbe.Categories) != 3 {
		t.Errorf("probe fixture = %+v", cProbe)
	}

	cDownloads, _ := p.compiled.Downloads(ctx, "music")
	pDownloads, err := p.port.Downloads(ctx, "music")
	if err != nil {
		t.Fatal(err)
	}
	p.same(t, "downloads", cDownloads, pDownloads)
	if len(pDownloads) != 6 || pDownloads[2].Name != "" || pDownloads[5].State != download.StateProcessing {
		t.Errorf("downloads fixture = %+v", pDownloads)
	}

	byRef := func(d download.Driver) ([]download.Download, error) {
		return d.(download.ByReference).DownloadsByRef(ctx, []string{"nzo_a1", "nzo_e5"})
	}
	cByRef, _ := byRef(p.compiled)
	pByRef, err := byRef(p.port)
	if err != nil {
		t.Fatal(err)
	}
	p.same(t, "downloads by reference", cByRef, pByRef)

	cQueue, _ := p.compiled.QueueStatus(ctx)
	pQueue, err := p.port.QueueStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.same(t, "queue status", cQueue, pQueue)

	for _, deleteFiles := range []bool{false, true} {
		cErr, pErr := p.compiled.Remove(ctx, "nzo_a1", deleteFiles), p.port.Remove(ctx, "nzo_a1", deleteFiles)
		p.same(t, "remove", cErr, pErr)
	}
	p.same(t, "pause", p.compiled.Pause(ctx, []string{"nzo_a1", "nzo_b2"}), p.port.Pause(ctx, []string{"nzo_a1", "nzo_b2"}))
	p.same(t, "resume", p.compiled.Resume(ctx, []string{"nzo_a1"}), p.port.Resume(ctx, []string{"nzo_a1"}))
}

func TestThePortUploadsTheSameFileAndReadsTheSameReference(t *testing.T) {
	p := newPair(t, sabAnswers)
	request := download.AddRequest{Name: "Quiet.Harbour-Night.Songs-2021", File: clienttest.NZB(), Category: "music"}
	cRef, cErr := p.compiled.Add(t.Context(), request)
	pRef, pErr := p.port.Add(t.Context(), request)
	if cErr != nil || pErr != nil || cRef != "nzo_h8" {
		t.Fatalf("add: %q %v / %q %v", cRef, cErr, pRef, pErr)
	}
	p.same(t, "add", cRef, pRef)
}

func TestThePortUploadCarriesTheFileUnderTheNamedField(t *testing.T) {
	server := sabServer(sabAnswers)
	client := specclient.New(loadPort(t), testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", "k")
	if _, err := client.AddNZB(t.Context(), download.AddRequest{Name: "Rel", File: clienttest.NZB(), Category: "music"}); err != nil {
		t.Fatal(err)
	}
	body := server.Seen[0].Body
	if !strings.Contains(body, `name="name"; filename="Rel.nzb"`) || !bytes.Contains([]byte(body), clienttest.NZB()) {
		t.Errorf("upload body = %.200q", body)
	}
}

func TestARefusedUploadIsErrRefusedWithTheClientsReason(t *testing.T) {
	answers := map[string]string{"addfile": `{"status":false,"error":"Duplicate NZB"}`}
	server := sabServer(answers)
	client := specclient.New(loadPort(t), testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", "k")
	_, err := client.AddNZB(t.Context(), download.AddRequest{Name: "Rel", File: clienttest.NZB()})
	if err == nil || !strings.Contains(err.Error(), "Duplicate NZB") || !isRefused(err) {
		t.Errorf("err = %v, want ErrRefused with the reason", err)
	}
}

func isRefused(err error) bool { return strings.Contains(err.Error(), download.ErrRefused.Error()) }
