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
