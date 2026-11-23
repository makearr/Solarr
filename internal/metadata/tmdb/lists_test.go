package tmdb_test

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

func TestListReadsMixedEntriesWithTheirKinds(t *testing.T) {
	transport := &canned{body: `{"id":"8","total_pages":3,"items":[
		{"id":1399,"media_type":"tv","name":"Example Show","first_air_date":"2011-04-17","poster_path":"/s.jpg"},
		{"id":550,"media_type":"movie","title":"Example Film","release_date":"1999-10-15"}]}`}
	page, err := newClient(t, transport, "plainkey").List(t.Context(), "8", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := tmdb.ListPage{TotalPages: 3, Items: []tmdb.ListItem{
		{ID: 1399, MediaType: "tv", Name: "Example Show", FirstAirDate: "2011-04-17", PosterPath: "/s.jpg"},
		{ID: 550, MediaType: "movie", Title: "Example Film", ReleaseDate: "1999-10-15"},
	}}
	if !reflect.DeepEqual(page, want) {
		t.Errorf("page = %+v, want %+v", page, want)
	}
	if got := transport.requested[0].URL; got.Path != "/3/list/8" || got.Query().Get("page") != "2" {
		t.Errorf("requested %s", got)
	}
}

func TestChartsMarkEveryEntryWithTheKindAskedFor(t *testing.T) {
	for name, read := range map[string]func(tmdb.Client) (tmdb.ListPage, error){
		"/3/trending/tv/week": func(c tmdb.Client) (tmdb.ListPage, error) {
			return c.Trending(t.Context(), tmdb.MediaTV, tmdb.TrendingWeek, 1)
		},
		"/3/tv/popular": func(c tmdb.Client) (tmdb.ListPage, error) { return c.Popular(t.Context(), tmdb.MediaTV, 1) },
	} {
		transport := &canned{body: `{"total_pages":500,"results":[{"id":7,"name":"Example Show"}]}`}
		page, err := read(newClient(t, transport, "plainkey"))
		if err != nil {
			t.Fatal(err)
		}
		want := tmdb.ListPage{TotalPages: 500, Items: []tmdb.ListItem{{ID: 7, MediaType: "tv", Name: "Example Show"}}}
		if !reflect.DeepEqual(page, want) || transport.requested[0].URL.Path != name {
			t.Errorf("%s: page = %+v, requested %s", name, page, transport.requested[0].URL)
		}
	}
}

func TestAMissingListIsNotFound(t *testing.T) {
	if _, err := newClient(t, &canned{status: http.StatusNotFound}, "plainkey").List(t.Context(), "9", 1); !errors.Is(err, tmdb.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, err := newClient(t, &canned{}, "").List(t.Context(), "9", 1); !errors.Is(err, tmdb.ErrNoKey) {
		t.Errorf("no key: err = %v", err)
	}
}

func TestDiscoverPushesTheQueryDownAndKeepsWhatTheChartCarries(t *testing.T) {
	transport := &canned{body: `{"total_pages":9,"results":[{"id":5,"title":"Example Film","release_date":"1980-02-01","genre_ids":[18,35],
		"original_language":"fr","vote_average":8.1,"vote_count":4200,"popularity":33.5,"overview":"An example."}]}`}
	page, err := newClient(t, transport, "plainkey").Discover(t.Context(), tmdb.MediaMovie, tmdb.DiscoverQuery{
		SortBy: tmdb.SortRating, MinVotes: 1000, ReleasedTo: "2000-11-23", WithoutGenres: []int64{99, 10764}, Language: "fr",
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := tmdb.ListPage{TotalPages: 9, Items: []tmdb.ListItem{{
		ID: 5, MediaType: "movie", Title: "Example Film", ReleaseDate: "1980-02-01", GenreIDs: []int64{18, 35}, OriginalLanguage: "fr",
		VoteAverage: 8.1, VoteCount: 4200, Popularity: 33.5, Overview: "An example.",
	}}}
	if !reflect.DeepEqual(page, want) {
		t.Errorf("page = %+v, want %+v", page, want)
	}
	got := transport.requested[0].URL
	query := got.Query()
	if got.Path != "/3/discover/movie" || query.Get("page") != "3" || query.Get("sort_by") != "vote_average.desc" || query.Get("vote_count.gte") != "1000" ||
		query.Get("primary_release_date.lte") != "2000-11-23" || query.Get("primary_release_date.gte") != "" || query.Get("without_genres") != "99|10764" ||
		query.Get("with_original_language") != "fr" || query.Get("include_adult") != "false" {
		t.Errorf("requested %s", got)
	}
}

func TestDiscoverTVDatesUseTheFirstAirDate(t *testing.T) {
	transport := &canned{body: `{"total_pages":1,"results":[]}`}
	if _, err := newClient(t, transport, "plainkey").Discover(t.Context(), tmdb.MediaTV, tmdb.DiscoverQuery{SortBy: tmdb.SortPopularity, ReleasedFrom: "2026-11-23"}, 1); err != nil {
		t.Fatal(err)
	}
	query := transport.requested[0].URL.Query()
	if transport.requested[0].URL.Path != "/3/discover/tv" || query.Get("first_air_date.gte") != "2026-11-23" || query.Get("vote_count.gte") != "" {
		t.Errorf("requested %s", transport.requested[0].URL)
	}
}
