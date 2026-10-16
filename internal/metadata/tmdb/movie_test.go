package tmdb_test

import (
	"strings"
	"testing"
)

func TestMovieReadsCreditsReleaseDatesAndCollection(t *testing.T) {
	transport := &canned{body: `{"id":603,"imdb_id":"tt0133093","title":"The Tonhut","original_title":"The Tonhut","overview":"Plot.",
		"release_date":"1999-03-30","runtime":136,"status":"Released","original_language":"en","vote_average":8.2,"vote_count":25000,
		"poster_path":"/p.jpg","backdrop_path":"/b.jpg","genres":[{"id":28,"name":"Action"}],
		"production_companies":[{"id":79,"name":"Village Roadshow","logo_path":"/l.png","origin_country":"US"}],
		"belongs_to_collection":{"id":2344,"name":"The Tonhut Collection","poster_path":"/c.jpg"},
		"credits":{"cast":[{"id":6384,"name":"Kaira Reeves","character":"Neo","profile_path":"/k.jpg","order":0}],
			"crew":[{"id":9340,"name":"Lana Wachowski","job":"Director","department":"Directing"}]},
		"release_dates":{"results":[
			{"iso_3166_1":"DE","release_dates":[{"certification":"16","type":3,"release_date":"1999-06-17T00:00:00.000Z"}]},
			{"iso_3166_1":"US","release_dates":[{"certification":"R","type":3,"release_date":"1999-03-31T00:00:00.000Z"},
				{"certification":"","type":5,"release_date":"2000-09-21T00:00:00.000Z"},{"certification":"","type":4,"release_date":"2010-01-01T00:00:00.000Z"}]}]},
		"keywords":{"keywords":[{"id":310,"name":"artificial intelligence"}]},
		"images":{"posters":[{"file_path":"/p2.jpg","iso_639_1":"en"}],"backdrops":[],"logos":[]},
		"alternative_titles":{"titles":[{"title":"Matrix","iso_3166_1":"DE"}]}}`}
	movie, err := newClient(t, transport, "plainkey").Movie(t.Context(), 603)
	if err != nil {
		t.Fatal(err)
	}
	if movie.IMDbID != "tt0133093" || movie.Collection == nil || movie.Collection.ID != 2344 || movie.Credits.Cast[0].Character != "Neo" {
		t.Errorf("movie = %+v", movie)
	}
	if got := movie.Certification("DE"); got != "16" {
		t.Errorf("DE certification = %q", got)
	}
	if got := movie.Certification("FR"); got != "R" {
		t.Errorf("fallback certification = %q, want the US one", got)
	}
	if got := movie.ReleaseOfType(4, 5); got != "2000-09-21" {
		t.Errorf("home release = %q, want the earlier of digital and physical", got)
	}
	if got := movie.ReleaseOfType(3); got != "1999-03-31" {
		t.Errorf("theatrical = %q, want the earliest across countries", got)
	}
	if !strings.Contains(transport.requested[0].URL.RawQuery, "append_to_response=credits") {
		t.Errorf("query = %s", transport.requested[0].URL.RawQuery)
	}
}

func TestSearchMoviesNarrowsByYearAndFindMapsAnIMDbID(t *testing.T) {
	transport := &canned{body: `{"results":[{"id":603,"title":"The Tonhut","release_date":"1999-03-30","poster_path":"/p.jpg"}]}`}
	hits, err := newClient(t, transport, "plainkey").SearchMovies(t.Context(), "the tonhut", 1999)
	if err != nil || len(hits) != 1 || hits[0].ID != 603 {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	if query := transport.requested[0].URL.Query(); query.Get("year") != "1999" || query.Get("query") != "the tonhut" {
		t.Errorf("query = %v", query)
	}
	found := &canned{body: `{"movie_results":[{"id":603,"title":"The Tonhut"}]}`}
	if hit, err := newClient(t, found, "plainkey").FindMovieByIMDb(t.Context(), "tt0133093"); err != nil || hit.ID != 603 {
		t.Errorf("find = %+v, %v", hit, err)
	}
	empty := &canned{body: `{"movie_results":[]}`}
	if _, err := newClient(t, empty, "plainkey").FindMovieByIMDb(t.Context(), "tt1"); err == nil {
		t.Error("an IMDb id TMDB does not map should be an error")
	}
}
