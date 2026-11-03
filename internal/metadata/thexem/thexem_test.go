package thexem_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/thexem"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Shapes as Sonarr's XEM client reads them, with invented ids; the live API refused the build host (Cloudflare, 2026-11-03).
const (
	haveMap = `{"result":"success","data":["70001",70003],"message":""}`
	fullMap = `{"result":"success","data":[
		{"scene":{"season":1,"episode":1,"absolute":1},"tvdb":{"season":1,"episode":1,"absolute":1}},
		{"scene":{"season":2,"episode":5,"absolute":30},"tvdb":{"season":1,"episode":30,"absolute":30},"tvdb_2":{"season":1,"episode":31,"absolute":31}}
	],"message":"full mapping for 70001 on tvdb"}`
	allNames = `{"result":"success","data":{"70001":[{"Example Show":-1},{"Example Show Second":2},"Example Show US",{"Example Show":"-1"}],"70002":[]},"message":""}`
	refusal  = `{"result":"failure","data":[],"message":"no show with the tvdb_id 70003 found"}`
)

type canned struct {
	bodies    map[string]string
	down      bool
	requested []string
}

func (c *canned) RoundTrip(request *http.Request) (*http.Response, error) {
	c.requested = append(c.requested, request.URL.String())
	status, body := http.StatusOK, ""
	switch found, known := c.bodies[request.URL.Path+"?"+request.URL.RawQuery]; {
	case c.down:
		// Cloudflare's refusal: final, so the limiter adds no back-off the fake clock would never end.
		status = http.StatusForbidden
	case known:
		body = found
	default:
		status = http.StatusNotFound
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

type memoryCache map[string]metacache.Entry

func (m memoryCache) Get(_ context.Context, kind, id string) (metacache.Entry, error) {
	entry, found := m[kind+"/"+id]
	if !found {
		return metacache.Entry{}, metacache.ErrNotFound
	}
	return entry, nil
}

func (m memoryCache) Set(_ context.Context, kind, id string, entry metacache.Entry) error {
	m[kind+"/"+id] = entry
	return nil
}

func newClient(t *testing.T, transport http.RoundTripper, fake *clock.Fake, cache memoryCache) thexem.Client {
	t.Helper()
	return thexem.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100}),
		httpx.ClientConfig{Provider: "thexem-test", UserAgent: "test", Transport: transport}), cache, fake)
}

func standard() *canned {
	return &canned{bodies: map[string]string{
		"/map/havemap?origin=tvdb":                  haveMap,
		"/map/all?origin=tvdb&id=70001":             fullMap,
		"/map/all?origin=tvdb&id=70003":             refusal,
		"/map/allNames?origin=tvdb&seasonNumbers=1": allNames,
	}}
}

func TestMappingsReadsSceneAndTVDBNumbersOfAMappedSeries(t *testing.T) {
	transport := standard()
	got, err := newClient(t, transport, clock.NewFakeAtEpoch(), memoryCache{}).Mappings(t.Context(), 70001)
	if err != nil {
		t.Fatal(err)
	}
	want := []thexem.Mapping{
		{TVDB: thexem.Number{Season: 1, Episode: 1, Absolute: 1}, Scene: thexem.Number{Season: 1, Episode: 1, Absolute: 1}},
		{TVDB: thexem.Number{Season: 1, Episode: 30, Absolute: 30}, Scene: thexem.Number{Season: 2, Episode: 5, Absolute: 30}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mappings = %+v, want %+v", got, want)
	}
	wantAsked := []string{"https://thexem.info/map/havemap?origin=tvdb", "https://thexem.info/map/all?origin=tvdb&id=70001"}
	if !reflect.DeepEqual(transport.requested, wantAsked) {
		t.Errorf("asked %v, want %v", transport.requested, wantAsked)
	}
}

func TestMappingsAsksNothingMoreForASeriesTheMapListLacks(t *testing.T) {
	transport := standard()
	got, err := newClient(t, transport, clock.NewFakeAtEpoch(), memoryCache{}).Mappings(t.Context(), 70002)
	if err != nil || got != nil {
		t.Fatalf("mappings = %+v, %v; want nil, nil", got, err)
	}
	if len(transport.requested) != 1 {
		t.Errorf("asked %v, want only the map list", transport.requested)
	}
}

func TestMappingsTakesARefusalAsNoMap(t *testing.T) {
	got, err := newClient(t, standard(), clock.NewFakeAtEpoch(), memoryCache{}).Mappings(t.Context(), 70003)
	if err != nil || got != nil {
		t.Fatalf("mappings = %+v, %v; want nil, nil", got, err)
	}
}

func TestCachedAnswersAreReusedWhileFreshAndOutliveAnOutage(t *testing.T) {
	transport, fake, cache := standard(), clock.NewFakeAtEpoch(), memoryCache{}
	client := newClient(t, transport, fake, cache)
	if _, err := client.Mappings(t.Context(), 70001); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Mappings(t.Context(), 70001); err != nil || len(transport.requested) != 2 {
		t.Fatalf("second read asked %v (%v), want the cache", transport.requested, err)
	}

	fake.Advance(thexem.FreshFor + time.Minute)
	transport.down = true
	got, err := client.Mappings(t.Context(), 70001)
	if err != nil || len(got) != 2 {
		t.Fatalf("stale read during an outage = %+v, %v; want the cached map", got, err)
	}
	if len(transport.requested) != 4 {
		t.Errorf("asked %v, want both calls tried again once stale", transport.requested)
	}
}

func TestAnOutageWithNothingCachedIsAnError(t *testing.T) {
	transport := standard()
	transport.down = true
	if _, err := newClient(t, transport, clock.NewFakeAtEpoch(), memoryCache{}).Mappings(t.Context(), 70001); err == nil {
		t.Fatal("want an error")
	}
}

func TestNamesKeepOnlyTheNamesForEverySeason(t *testing.T) {
	client := newClient(t, standard(), clock.NewFakeAtEpoch(), memoryCache{})
	got, err := client.Names(t.Context(), 70001)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Example Show", "Example Show US"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names = %q, want %q", got, want)
	}
	if got, err := client.Names(t.Context(), 70002); err != nil || len(got) != 0 {
		t.Errorf("unnamed series = %q, %v; want none", got, err)
	}
}

func TestPingReportsARefusal(t *testing.T) {
	transport := &canned{bodies: map[string]string{"/map/havemap?origin=tvdb": refusal}}
	if err := newClient(t, transport, clock.NewFakeAtEpoch(), memoryCache{}).Ping(t.Context()); !errors.Is(err, thexem.ErrRefused) {
		t.Errorf("ping = %v, want ErrRefused", err)
	}
	if err := newClient(t, standard(), clock.NewFakeAtEpoch(), memoryCache{}).Ping(t.Context()); err != nil {
		t.Errorf("ping = %v, want nil", err)
	}
}
