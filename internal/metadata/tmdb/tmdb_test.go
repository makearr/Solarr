package tmdb_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/tmdb"
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
	return &http.Response{StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(c.body)), Request: request}, nil
}

func newClient(t *testing.T, transport http.RoundTripper, key string) tmdb.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return tmdb.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100}),
		httpx.ClientConfig{Provider: "tmdb-test", UserAgent: "test", Transport: transport}), func() string { return key }, "")
}

func TestSeriesReadsCreditsIDsRatingsAndOrderings(t *testing.T) {
	transport := &canned{body: `{"id":7,"name":"Example Show","original_name":"Beispiel","overview":"Plot.","first_air_date":"2020-01-05",
		"status":"Ended","episode_run_time":[40],"origin_country":["US"],"original_language":"en","vote_average":7.5,"vote_count":30,
		"poster_path":"/p.jpg","backdrop_path":"/b.jpg","genres":[{"id":1,"name":"Drama"}],
		"networks":[{"id":9,"name":"Example Net","logo_path":"/n.png","origin_country":"US"}],
		"created_by":[{"id":5,"name":"A Creator","profile_path":"/c.jpg"}],
		"seasons":[{"season_number":1,"episode_count":8,"poster_path":"/s1.jpg"}],
		"external_ids":{"imdb_id":"tt0000042","tvdb_id":1234},
		"aggregate_credits":{"cast":[{"id":11,"name":"A Person","profile_path":"/a.jpg","order":0,"total_episode_count":8,"roles":[{"character":"A Role","episode_count":8}]}],
			"crew":[{"id":12,"name":"B Person","department":"Directing","total_episode_count":3,"jobs":[{"job":"Director","episode_count":3}]}]},
		"content_ratings":{"results":[{"iso_3166_1":"DE","rating":"12"},{"iso_3166_1":"US","rating":"TV-14"}]},
		"keywords":{"results":[{"id":3,"name":"heist"}]},
		"images":{"posters":[{"file_path":"/p2.jpg","iso_639_1":"en","vote_average":5.1,"width":1000,"height":1500}],"backdrops":[],"logos":[{"file_path":"/l.png","iso_639_1":"en"}]},
		"alternative_titles":{"results":[{"title":"Der Beispielshow","iso_3166_1":"DE"}]},
		"episode_groups":{"results":[{"id":"abc","name":"DVD order","type":3,"episode_count":9,"group_count":1}]}}`}
	series, err := newClient(t, transport, "plainkey").Series(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if series.ExternalIDs.TVDB != 1234 || series.ExternalIDs.IMDb != "tt0000042" || series.Networks[0].ID != 9 {
		t.Errorf("series = %+v", series)
	}
	if cast := series.AggregateCredits.Cast[0]; cast.ID != 11 || cast.Roles[0].Character != "A Role" || cast.TotalEpisodeCount != 8 {
		t.Errorf("cast = %+v", cast)
	}
	if crew := series.AggregateCredits.Crew[0]; crew.Jobs[0].Job != "Director" || crew.Department != "Directing" {
		t.Errorf("crew = %+v", crew)
	}
	if got := series.ContentRating("DE"); got != "12" {
		t.Errorf("DE rating = %q", got)
	}
	if got := series.ContentRating("FR"); got != "TV-14" {
		t.Errorf("fallback rating = %q, want the US one", got)
	}
	if series.EpisodeGroups.Results[0].ID != "abc" || len(series.Images.Logos) != 1 {
		t.Errorf("groups/images = %+v %+v", series.EpisodeGroups, series.Images)
	}
}

func TestAPIKeyGoesAsParameterAndTokenAsBearer(t *testing.T) {
	plain := &canned{body: `{"results":[]}`}
	if _, err := newClient(t, plain, "abc123").Search(t.Context(), "x"); err != nil {
		t.Fatal(err)
	}
	if got := plain.requested[0].URL.Query().Get("api_key"); got != "abc123" || plain.requested[0].Header.Get("Authorization") != "" {
		t.Errorf("v3 key: api_key=%q auth=%q", got, plain.requested[0].Header.Get("Authorization"))
	}
	token := &canned{body: `{"results":[]}`}
	if _, err := newClient(t, token, "eyJhbGciOiJIUzI1NiJ9.payload.sig").Search(t.Context(), "x"); err != nil {
		t.Fatal(err)
	}
	if token.requested[0].URL.Query().Has("api_key") || token.requested[0].Header.Get("Authorization") != "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig" {
		t.Errorf("v4 token: url=%s auth=%q", token.requested[0].URL, token.requested[0].Header.Get("Authorization"))
	}
}

func TestNoKeyAsksNothing(t *testing.T) {
	transport := &canned{}
	_, err := newClient(t, transport, "  ").Series(t.Context(), 7)
	if !errors.Is(err, tmdb.ErrNoKey) || len(transport.requested) != 0 {
		t.Errorf("err = %v, requests = %d", err, len(transport.requested))
	}
}

func TestRefusedKeyAndUnknownShowAreTold(t *testing.T) {
	if err := newClient(t, &canned{status: http.StatusUnauthorized}, "k").Ping(t.Context()); !errors.Is(err, tmdb.ErrBadKey) {
		t.Errorf("401 err = %v", err)
	}
	if _, err := newClient(t, &canned{status: http.StatusNotFound}, "k").Series(t.Context(), 1); !errors.Is(err, tmdb.ErrNotFound) {
		t.Errorf("404 err = %v", err)
	}
}

func TestFindMapsATVDBIDAndSaysWhenNone(t *testing.T) {
	hit, err := newClient(t, &canned{body: `{"tv_results":[{"id":7,"name":"Example Show"}]}`}, "k").FindByTVDB(t.Context(), 1234)
	if err != nil || hit.ID != 7 {
		t.Errorf("hit = %+v, err = %v", hit, err)
	}
	if _, err := newClient(t, &canned{body: `{"tv_results":[]}`}, "k").FindByTVDB(t.Context(), 1); !errors.Is(err, tmdb.ErrNotFound) {
		t.Errorf("empty err = %v", err)
	}
}

func TestSeasonCarriesGuestStarsAndEpisodeCrew(t *testing.T) {
	season, err := newClient(t, &canned{body: `{"season_number":1,"episodes":[{"id":100,"season_number":1,"episode_number":2,"name":"Two","still_path":"/st.jpg",
		"guest_stars":[{"id":21,"name":"G Star","character":"A Guest","order":500}],"crew":[{"id":22,"name":"W Rider","job":"Writer","department":"Writing"}]}]}`}, "k").Season(t.Context(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	episode := season.Episodes[0]
	if episode.GuestStars[0].Character != "A Guest" || episode.Crew[0].Job != "Writer" || tmdb.ImageURL(episode.StillPath) != "https://image.tmdb.org/t/p/original/st.jpg" {
		t.Errorf("episode = %+v", episode)
	}
}

func TestPersonReadsBiographyAndCredits(t *testing.T) {
	person, err := newClient(t, &canned{body: `{"id":11,"name":"A Person","biography":"Bio.","birthday":"1980-02-03","place_of_birth":"Town","profile_path":"/a.jpg",
		"known_for_department":"Acting","also_known_as":["Per Son"],"external_ids":{"imdb_id":"nm0000001"},
		"combined_credits":{"cast":[{"id":7,"media_type":"tv","name":"Example Show","character":"A Role","first_air_date":"2020-01-05","episode_count":8}],"crew":[]}}`}, "k").Person(t.Context(), 11)
	if err != nil {
		t.Fatal(err)
	}
	if person.Birthday != "1980-02-03" || person.ExternalIDs.IMDb != "nm0000001" || person.CombinedCredits.Cast[0].EpisodeCount != 8 {
		t.Errorf("person = %+v", person)
	}
}

func TestNewOnNetworkAsksForShowsSinceADate(t *testing.T) {
	transport := &canned{body: `{"results":[{"id":7,"name":"New Show","first_air_date":"2026-09-01"}]}`}
	hits, err := newClient(t, transport, "k").NewOnNetwork(t.Context(), 9, "2026-08-01")
	if err != nil || len(hits) != 1 || hits[0].FirstAirDate != "2026-09-01" {
		t.Fatalf("hits = %+v, err = %v", hits, err)
	}
	query := transport.requested[0].URL.Query()
	if query.Get("with_networks") != "9" || query.Get("first_air_date.gte") != "2026-08-01" || query.Get("sort_by") != "first_air_date.desc" {
		t.Errorf("query = %v", query)
	}
}
