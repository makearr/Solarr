package kitsu_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/kitsu"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	status, body := http.StatusOK, `{"data":[],"included":[]}`
	switch {
	case request.URL.Path == "/api/edge/mappings" && request.URL.Query().Get("filter[externalId]") == "77":
		body = `{"data":[{"id":"1","type":"mappings","attributes":{}}],"included":[{"id":"900","type":"anime","attributes":{"slug":"example-show","canonicalTitle":"Example Show","synopsis":"A story.","ageRating":"PG","averageRating":"83.03","userCount":1200,"startDate":"2006-10-04","episodeCount":12,"episodeLength":24,"posterImage":{"original":"https://media.example/poster.jpg"}}}]}`
	case request.URL.Path == "/api/edge/anime/900":
		body = `{"data":{"id":"900","type":"anime","attributes":{}},"included":[{"id":"1","type":"mappings","attributes":{"externalSite":"myanimelist/anime","externalId":"55"}},{"id":"2","type":"mappings","attributes":{"externalSite":"anidb","externalId":"146"}}]}`
	case request.URL.Path == "/api/edge/anime/404":
		status, body = http.StatusNotFound, `{}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestAnAniListIDFindsTheEntryAndItsMappings(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "kitsu", UserAgent: "test", Transport: answer{}})
	got, err := kitsu.New(client).ByAniList(t.Context(), 77)
	want := kitsu.Anime{ID: "900", Slug: "example-show", Title: "Example Show", Synopsis: "A story.", AgeRating: "PG", AverageRating: "83.03", UserCount: 1200,
		StartDate: "2006-10-04", EpisodeCount: 12, EpisodeLength: 24, Poster: "https://media.example/poster.jpg",
		Mappings: map[string]string{"myanimelist/anime": "55", "anidb": "146"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("anime = %+v, %v\nwant %+v", got, err, want)
	}
	if _, err := kitsu.New(client).ByAniList(t.Context(), 1); !errors.Is(err, kitsu.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}
