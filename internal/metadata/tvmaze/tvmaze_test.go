package tvmaze_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type canned struct {
	status    int
	body      string
	requested []string
}

func (c *canned) RoundTrip(request *http.Request) (*http.Response, error) {
	c.requested = append(c.requested, request.URL.String())
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(c.body)), Request: request}, nil
}

func newClient(t *testing.T, transport http.RoundTripper) tvmaze.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return tvmaze.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100}),
		httpx.ClientConfig{Provider: "tvmaze-test", UserAgent: "test", Transport: transport}))
}

// Shapes as the live API answered them 2026-10-02, with an invented show.
func TestShowReadsNetworkTimezoneAndExternalIDs(t *testing.T) {
	transport := &canned{body: `{"id":42,"name":"Example Show","type":"Scripted","language":"English","genres":["Drama"],
		"status":"Running","runtime":60,"averageRuntime":58,"premiered":"2020-01-05","ended":null,
		"schedule":{"time":"22:00","days":["Sunday"]},"rating":{"average":7.5},
		"network":{"id":2,"name":"Example Network","country":{"name":"United States","code":"US","timezone":"America/New_York"}},
		"webChannel":null,"externals":{"tvrage":null,"thetvdb":1234,"imdb":"tt0000042"},
		"image":{"medium":"https://example.test/m.jpg","original":"https://example.test/o.jpg"},"summary":"<p>Plot.</p>","updated":1}`}
	show, err := newClient(t, transport).Show(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if show.Network == nil || show.Network.Country.Timezone != "America/New_York" || show.WebChannel != nil {
		t.Errorf("network = %+v", show.Network)
	}
	if show.Externals.TheTVDB == nil || *show.Externals.TheTVDB != 1234 || show.Externals.TVRage != nil || show.Externals.IMDb != "tt0000042" {
		t.Errorf("externals = %+v", show.Externals)
	}
	if transport.requested[0] != "https://api.tvmaze.com/shows/42" {
		t.Errorf("asked %s", transport.requested[0])
	}
}

func TestEpisodesKeepUnnumberedSpecialsAndAirstamps(t *testing.T) {
	transport := &canned{body: `[{"id":1,"name":"Pilot","season":1,"number":1,"type":"regular","airdate":"2020-01-05","airtime":"22:00","airstamp":"2020-01-06T03:00:00+00:00","runtime":60},
		{"id":2,"name":"Behind the scenes","season":1,"number":null,"type":"insignificant_special","airdate":"2020-01-04","airtime":"","airstamp":null,"runtime":10}]`}
	episodes, err := newClient(t, transport).Episodes(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 2 || episodes[0].Number == nil || *episodes[0].Number != 1 || episodes[1].Number != nil || episodes[1].Airstamp != "" {
		t.Errorf("episodes = %+v", episodes)
	}
	if transport.requested[0] != "https://api.tvmaze.com/shows/42/episodes?specials=1" {
		t.Errorf("asked %s", transport.requested[0])
	}
}

func TestLookupAndNotFound(t *testing.T) {
	missing := &canned{status: http.StatusNotFound, body: ""}
	if _, err := newClient(t, missing).LookupTVDB(t.Context(), 999); !errors.Is(err, tvmaze.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if missing.requested[0] != "https://api.tvmaze.com/lookup/shows?thetvdb=999" {
		t.Errorf("asked %s", missing.requested[0])
	}
	failing := &canned{status: http.StatusInternalServerError}
	if _, err := newClient(t, failing).Show(t.Context(), 1); err == nil || errors.Is(err, tvmaze.ErrNotFound) {
		t.Errorf("err = %v, want a status error", err)
	}
}

func TestSearchAndAliases(t *testing.T) {
	search := &canned{body: `[{"score":0.9,"show":{"id":7,"name":"Alpha"}}]`}
	results, err := newClient(t, search).Search(t.Context(), "alpha beta")
	if err != nil || len(results) != 1 || results[0].Show.ID != 7 || search.requested[0] != "https://api.tvmaze.com/search/shows?q=alpha+beta" {
		t.Errorf("results = %+v, %v, asked %v", results, err, search.requested)
	}
	akas := &canned{body: `[{"name":"Alt One","country":null},{"name":"Alt Two"}]`}
	names, err := newClient(t, akas).Aliases(t.Context(), 7)
	if err != nil || strings.Join(names, ",") != "Alt One,Alt Two" {
		t.Errorf("aliases = %v, %v", names, err)
	}
}
