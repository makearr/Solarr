package skyhook_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/skyhook"
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

func newClient(t *testing.T, transport http.RoundTripper) skyhook.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return skyhook.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100}),
		httpx.ClientConfig{Provider: "skyhook-test", UserAgent: "test", Transport: transport}), "")
}

// Shapes as the live proxy answered them 2026-10-02, with an invented show.
func TestSeriesReadsIDsEpisodesAndSpecialPlacement(t *testing.T) {
	transport := &canned{body: `{"tvdbId":1234,"title":"Example Show","overview":"Plot.","slug":"example-show","originalCountry":"usa",
		"originalLanguage":"eng","language":"eng","firstAired":"2020-01-05","lastAired":"2021-03-01","tvRageId":0,"tvMazeId":42,
		"tmdbId":7,"imdbId":"tt0000042","malIds":[],"anidbIds":[3],"aniListIds":[],"lastUpdated":"2026-09-03T15:52:42Z","status":"Ended",
		"runtime":40,"timeOfDay":{"hours":22,"minutes":0},"originalNetwork":"Example Net","network":"Example Net","genres":["Drama"],
		"contentRating":"TV-14","rating":{"count":10,"value":"6.5"},"actors":[{"name":"A Person","character":"A Role","image":"https://example.test/a.jpg"}],
		"images":[{"coverType":"Poster","url":"https://example.test/p.jpg"}],
		"seasons":[{"seasonNumber":0,"images":[]},{"seasonNumber":1,"images":[{"coverType":"Poster","url":"https://example.test/s1.jpg"}]}],
		"episodes":[{"tvdbShowId":1234,"tvdbId":1,"seasonNumber":0,"episodeNumber":1,"airedBeforeSeasonNumber":1,"airedBeforeEpisodeNumber":1,"title":"Recap","airDate":"2019-12-31","airDateUtc":"2020-01-01T02:00:00Z","runtime":45},
		{"tvdbShowId":1234,"tvdbId":2,"seasonNumber":1,"episodeNumber":1,"absoluteEpisodeNumber":1,"title":"Pilot","airDate":"2020-01-05","runtime":40,"finaleType":"season"}]}`}
	series, err := newClient(t, transport).Series(t.Context(), 1234)
	if err != nil {
		t.Fatal(err)
	}
	if series.TVMazeID != 42 || series.TMDBID != 7 || len(series.AniDBIDs) != 1 || series.TimeOfDay == nil || series.TimeOfDay.Hours != 22 {
		t.Errorf("series = %+v", series)
	}
	special, pilot := series.Episodes[0], series.Episodes[1]
	if special.AiredBeforeSeasonNumber == nil || *special.AiredBeforeSeasonNumber != 1 || pilot.AbsoluteEpisodeNumber == nil || pilot.AiredBeforeSeasonNumber != nil {
		t.Errorf("episodes = %+v", series.Episodes)
	}
	if transport.requested[0] != "https://skyhook.sonarr.tv/v1/tvdb/shows/en/1234" {
		t.Errorf("asked %s", transport.requested[0])
	}
}

func TestSearchAndNotFound(t *testing.T) {
	search := &canned{body: `[{"tvdbId":5,"title":"Alpha"}]`}
	hits, err := newClient(t, search).Search(t.Context(), "alpha beta")
	if err != nil || len(hits) != 1 || hits[0].TVDBID != 5 || search.requested[0] != "https://skyhook.sonarr.tv/v1/tvdb/search/en/?term=alpha+beta" {
		t.Errorf("hits = %+v, %v, asked %v", hits, err, search.requested)
	}
	if _, err := newClient(t, &canned{status: http.StatusNotFound}).Series(t.Context(), 1); !errors.Is(err, skyhook.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
