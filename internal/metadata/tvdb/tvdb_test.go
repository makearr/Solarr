package tvdb_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type routed struct {
	answers  map[string]string
	statuses map[string][]int
	seen     []string
	logins   int
	auth     []string
}

func (r *routed) RoundTrip(request *http.Request) (*http.Response, error) {
	path := request.URL.Path
	if strings.HasSuffix(path, "/login") {
		r.logins++
	}
	r.seen = append(r.seen, request.Method+" "+path+"?"+request.URL.RawQuery)
	r.auth = append(r.auth, request.Header.Get("Authorization"))
	status := http.StatusOK
	if queue := r.statuses[path]; len(queue) > 0 {
		status, r.statuses[path] = queue[0], queue[1:]
	}
	body, ok := r.answers[path+"?"+request.URL.RawQuery]
	if !ok {
		body = r.answers[path]
	}
	return &http.Response{StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newClient(t *testing.T, transport *routed, key string) tvdb.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return tvdb.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100}),
		httpx.ClientConfig{Provider: "tvdb-test", UserAgent: "test", Transport: transport}),
		func() string { return key }, nil, func() time.Time { return fake.Now() })
}

const loginAnswer = `{"status":"success","data":{"token":"tok-1"}}`

func TestSeriesLogsInOnceAndReadsTheExtendedAnswer(t *testing.T) {
	transport := &routed{answers: map[string]string{
		"/v4/login": loginAnswer,
		"/v4/series/42/extended": `{"data":{"id":42,"name":"Example Show","overview":"Plot.","firstAired":"2020-01-05","status":{"id":2,"name":"Ended"},
			"averageRuntime":40,"originalCountry":"usa","originalLanguage":"eng","airsTime":"21:00",
			"genres":[{"id":10,"name":"Drama"}],"originalNetwork":{"id":5,"name":"Example Net","country":"usa"},
			"remoteIds":[{"id":"tt0000042","type":2,"sourceName":"IMDB"},{"id":"7","type":12,"sourceName":"TheMovieDB.com"}],
			"contentRatings":[{"name":"TV-14","country":"usa"}],
			"seasons":[{"id":1,"number":1,"type":{"id":1,"name":"Aired Order","type":"official"}}],
			"characters":[{"id":9,"name":"A Role","peopleId":77,"personName":"A Person","peopleType":"Actor","sort":1}]}}`}}
	client := newClient(t, transport, "project-key")
	series, err := client.Series(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Series(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	if transport.logins != 1 {
		t.Errorf("logins = %d, want the token reused", transport.logins)
	}
	if series.RemoteID("imdb") != "tt0000042" || series.RemoteID("TheMovieDB.com") != "7" || series.Characters[0].PeopleType != "Actor" ||
		series.Seasons[0].Type.Type != "official" || series.Status.Name != "Ended" {
		t.Errorf("series = %+v", series)
	}
	if got := transport.auth[1]; got != "Bearer tok-1" {
		t.Errorf("series request auth = %q", got)
	}
}

func TestExpiredTokenIsRenewedOnce(t *testing.T) {
	transport := &routed{
		answers:  map[string]string{"/v4/login": loginAnswer, "/v4/series/42/extended": `{"data":{"id":42}}`},
		statuses: map[string][]int{"/v4/series/42/extended": {http.StatusUnauthorized}},
	}
	if _, err := newClient(t, transport, "k").Series(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	if transport.logins != 2 {
		t.Errorf("logins = %d, want a second after the 401", transport.logins)
	}
}

func TestEpisodesFollowThePages(t *testing.T) {
	transport := &routed{answers: map[string]string{
		"/v4/login":                         loginAnswer,
		"/v4/series/42/episodes/dvd?page=0": `{"data":{"episodes":[{"id":1,"name":"One","seasonNumber":1,"number":1}]},"links":{"next":"/x?page=1"}}`,
		"/v4/series/42/episodes/dvd?page=1": `{"data":{"episodes":[{"id":2,"name":"Two","seasonNumber":1,"number":2,"absoluteNumber":2}]},"links":{"next":null}}`,
	}}
	episodes, err := newClient(t, transport, "k").Episodes(t.Context(), 42, "dvd")
	if err != nil || len(episodes) != 2 || episodes[1].AbsoluteNumber != 2 {
		t.Errorf("episodes = %+v, err = %v", episodes, err)
	}
}

func TestRefusedKeyAndMissingKeyAreBadKey(t *testing.T) {
	refused := &routed{statuses: map[string][]int{"/v4/login": {http.StatusUnauthorized}}, answers: map[string]string{}}
	if err := newClient(t, refused, "wrong").Ping(t.Context()); !errors.Is(err, tvdb.ErrBadKey) {
		t.Errorf("refused err = %v", err)
	}
	none := &routed{answers: map[string]string{}}
	if err := newClient(t, none, "").Ping(t.Context()); !errors.Is(err, tvdb.ErrBadKey) || len(none.seen) != 0 {
		t.Errorf("no key err = %v, requests = %v", err, none.seen)
	}
}

func TestUnknownSeriesIsNotFound(t *testing.T) {
	transport := &routed{
		answers:  map[string]string{"/v4/login": loginAnswer, "/v4/series/9/extended": `{"status":"failure","data":null}`},
		statuses: map[string][]int{"/v4/series/9/extended": {http.StatusNotFound}},
	}
	if _, err := newClient(t, transport, "k").Series(t.Context(), 9); !errors.Is(err, tvdb.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
