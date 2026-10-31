package theporndb_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/theporndb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct {
	body   string
	status int
	asked  []*http.Request
}

func (a *answer) RoundTrip(request *http.Request) (*http.Response, error) {
	a.asked = append(a.asked, request)
	status := a.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(a.body)), Request: request}, nil
}

func client(t *testing.T, transport http.RoundTripper, key string) theporndb.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return theporndb.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "theporndb", UserAgent: "test", Transport: transport}), func() string { return key })
}

func TestSearchMoviesSendsTheBearerKeyAndReadsTheFilm(t *testing.T) {
	transport := &answer{body: `{"data":[{"id":"m-1","title":"Big Film Vol. 3","date":"2017-03-13","duration":7980,"image":"https://img/c.jpg",
		"site":{"uuid":"s-1","name":"Night Label","network":{"uuid":"n-1","name":"Night Network"}},
		"performers":[{"id":"site-p","name":"Ada V.","parent":{"id":"p-1","name":"Ada Vale","extra":{"gender":"Female"}}}],
		"tags":[{"name":"Anal"}],"directors":[]}]}`}
	movies, err := client(t, transport, " key ").SearchMovies(t.Context(), "big film", 5)
	if err != nil || len(movies) != 1 {
		t.Fatalf("movies = %+v, %v", movies, err)
	}
	film := movies[0]
	if film.Title != "Big Film Vol. 3" || film.Duration != 7980 || film.Site.Network.Name != "Night Network" || film.Tags[0].Name != "Anal" {
		t.Errorf("film = %+v", film)
	}
	if id, name, _, gender := film.Performers[0].Canonical(); id != "p-1" || name != "Ada Vale" || gender != "Female" {
		t.Errorf("canonical performer = %s %s %s", id, name, gender)
	}
	if got := transport.asked[0].Header.Get("Authorization"); got != "Bearer key" {
		t.Errorf("Authorization = %q", got)
	}
	if got := transport.asked[0].URL.RawQuery; got != "per_page=5&q=big+film" {
		t.Errorf("query = %q", got)
	}
}

func TestMovieNotFoundAndNoKey(t *testing.T) {
	if _, err := client(t, &answer{status: http.StatusNotFound, body: `{}`}, "k").Movie(t.Context(), "x"); !errors.Is(err, theporndb.ErrNotFound) {
		t.Errorf("404 err = %v", err)
	}
	if _, err := client(t, &answer{body: `{"data":{}}`}, "k").Movie(t.Context(), "x"); !errors.Is(err, theporndb.ErrNotFound) {
		t.Errorf("empty film err = %v", err)
	}
	if _, err := client(t, &answer{}, "").SearchMovies(t.Context(), "x", 1); !errors.Is(err, theporndb.ErrNoKey) {
		t.Errorf("no key err = %v", err)
	}
}
