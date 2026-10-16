package radarrmeta_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/radarrmeta"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type canned struct {
	status    int
	body      string
	requested []*http.Request
}

func (c *canned) RoundTrip(request *http.Request) (*http.Response, error) {
	c.requested = append(c.requested, request)
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(c.body)), Request: request}, nil
}

func newClient(t *testing.T, transport http.RoundTripper) radarrmeta.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return radarrmeta.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100}),
		httpx.ClientConfig{Provider: "radarrmeta-test", UserAgent: "test", Transport: transport}))
}

func TestMovieReadsDatesCreditsAndCollection(t *testing.T) {
	transport := &canned{body: `{"TmdbId":603,"ImdbId":"tt0133093","Title":"The Tonhut","Year":1999,"Runtime":136,
		"InCinema":"1999-03-31T00:00:00Z","PhysicalRelease":"1999-11-25T00:00:00Z","DigitalRelease":"2001-03-19T00:00:00Z",
		"Genres":["Action"],"Studio":"Village Roadshow Pictures","Certifications":[{"Country":"US","Certification":"R"}],
		"MovieRatings":{"Imdb":{"Count":2285560,"Value":8.7}},
		"Credits":{"Cast":[{"Name":"Kaira Reeves","Order":0,"Character":"Neo","TmdbId":6384}],"Crew":[{"Name":"Lana Wachowski","Job":"Director","Department":"Directing","TmdbId":9340}]},
		"Collection":{"TmdbId":2344,"Name":"The Tonhut Collection"}}`}
	movie, err := newClient(t, transport).Movie(t.Context(), 603)
	if err != nil {
		t.Fatal(err)
	}
	if movie.TMDBID != 603 || movie.InCinemas != "1999-03-31T00:00:00Z" || movie.Credits.Cast[0].Character != "Neo" ||
		movie.Collection == nil || movie.Collection.TMDBID != 2344 || movie.MovieRatings["Imdb"].Value != 8.7 {
		t.Errorf("movie = %+v", movie)
	}
	if got := transport.requested[0].URL.Path; got != "/v1/movie/603" {
		t.Errorf("path = %s", got)
	}
}

func TestSearchAndByIMDbAndNotFound(t *testing.T) {
	transport := &canned{body: `[{"TmdbId":603,"Title":"The Tonhut","Year":1999}]`}
	client := newClient(t, transport)
	hits, err := client.Search(t.Context(), "the tonhut", 1999)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	if query := transport.requested[0].URL.Query(); query.Get("q") != "the tonhut" || query.Get("year") != "1999" {
		t.Errorf("query = %v", query)
	}
	if movie, err := client.ByIMDb(t.Context(), "tt0133093"); err != nil || movie.TMDBID != 603 {
		t.Errorf("by imdb = %+v, %v", movie, err)
	}
	if _, err := newClient(t, &canned{body: `[]`}).ByIMDb(t.Context(), "tt1"); !errors.Is(err, radarrmeta.ErrNotFound) {
		t.Errorf("an unknown IMDb id: err = %v, want ErrNotFound", err)
	}
	if _, err := newClient(t, &canned{status: 404}).Movie(t.Context(), 1); !errors.Is(err, radarrmeta.ErrNotFound) {
		t.Errorf("404: err = %v, want ErrNotFound", err)
	}
}
