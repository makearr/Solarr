package tmdbfeeds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tmdbfeeds"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type charts struct {
	trending, discovered []string
	query                tmdb.DiscoverQuery
	page                 int
	answer               tmdb.ListPage
	err                  error
}

func (c *charts) Discover(_ context.Context, media string, query tmdb.DiscoverQuery, page int) (tmdb.ListPage, error) {
	c.discovered, c.query, c.page = append(c.discovered, media), query, page
	return c.answer, c.err
}

func (c *charts) Trending(_ context.Context, media, window string, page int) (tmdb.ListPage, error) {
	c.trending, c.page = append(c.trending, media+"/"+window), page
	return c.answer, c.err
}

func feeds(c *charts, media string) tmdbfeeds.Feeds {
	return tmdbfeeds.Feeds{Media: media, Charts: c, Clock: clock.NewFake(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)), Configured: func() bool { return true }}
}

func TestFeedsAreUnavailableWithoutAKeyAndSayWhy(t *testing.T) {
	f := feeds(&charts{}, tmdb.MediaMovie)
	f.Configured = func() bool { return false }
	usable, unavailable := f.RecommendationFeeds(t.Context())
	if len(usable) != 0 || len(unavailable) != 4 {
		t.Fatalf("usable %v, unavailable %v", usable, unavailable)
	}
	for _, reason := range unavailable {
		if reason != tmdbfeeds.NoKeyReason {
			t.Errorf("reason = %q", reason)
		}
	}
}

func TestEachFeedAsksTMDBForItsOwnChart(t *testing.T) {
	cases := []struct {
		feed  media.RecommendationType
		media string
		want  tmdb.DiscoverQuery
	}{
		{media.RecommendHighestRated, tmdb.MediaMovie, tmdb.DiscoverQuery{SortBy: "vote_average.desc", MinVotes: 500, WithoutGenres: []int64{35, 10764}, Language: "en"}},
		{media.RecommendHighestRated, tmdb.MediaTV, tmdb.DiscoverQuery{SortBy: "vote_average.desc", MinVotes: 200, WithoutGenres: []int64{35, 10764}, Language: "en"}},
		{media.RecommendUpcoming, tmdb.MediaMovie, tmdb.DiscoverQuery{SortBy: "popularity.desc", ReleasedFrom: "2026-11-23", WithoutGenres: []int64{35, 10764}, Language: "en"}},
		{media.RecommendClassics, tmdb.MediaMovie, tmdb.DiscoverQuery{SortBy: "vote_average.desc", MinVotes: 1000, ReleasedTo: "2001-11-23", WithoutGenres: []int64{35, 10764}, Language: "en"}},
		{media.RecommendClassics, tmdb.MediaTV, tmdb.DiscoverQuery{SortBy: "vote_average.desc", MinVotes: 300, ReleasedTo: "2001-11-23", WithoutGenres: []int64{35, 10764}, Language: "en"}},
	}
	for _, c := range cases {
		source := &charts{}
		_, _, err := feeds(source, c.media).Recommend(t.Context(), media.RecommendQuery{
			Feed: media.RecommendationFeed{Type: c.feed}, Page: 2, ClassicAgeYears: 25, Languages: []string{"en"}, ExcludeGenres: []string{"comedy", "Reality", "Unheard Of"},
		})
		if err != nil || !reflect.DeepEqual(source.query, c.want) || source.discovered[0] != c.media || source.page != 2 {
			t.Errorf("%s %s: query %+v page %d err %v, want %+v", c.media, c.feed, source.query, source.page, err, c.want)
		}
	}
	source := &charts{}
	if _, _, err := feeds(source, tmdb.MediaTV).Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending}, Page: 1}); err != nil ||
		!reflect.DeepEqual(source.trending, []string{"tv/week"}) {
		t.Errorf("trending asked %v, %v", source.trending, err)
	}
	if _, _, err := feeds(source, tmdb.MediaTV).Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendCollections}, Page: 1}); err == nil {
		t.Error("collections were asked of a type")
	}
}

func TestSeveralLanguagesAreNotPushedDown(t *testing.T) {
	source := &charts{}
	_, _, _ = feeds(source, tmdb.MediaMovie).Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendUpcoming}, Page: 1, Languages: []string{"en", "ja"}})
	if source.query.Language != "" {
		t.Errorf("language = %q", source.query.Language)
	}
}

func TestAChartRowBecomesARecommendationWithItsDetailAndMore(t *testing.T) {
	source := &charts{answer: tmdb.ListPage{TotalPages: 3, Items: []tmdb.ListItem{{
		ID: 42, Name: "Example Show", FirstAirDate: "2019-03-02", PosterPath: "/p.jpg", Overview: "About it.", GenreIDs: []int64{18, 10765}, OriginalLanguage: "ja",
		OriginCountry: []string{"JP"}, VoteAverage: 8.4, VoteCount: 900, Popularity: 55,
	}}}}
	f := feeds(source, tmdb.MediaTV)
	f.Detail = func(_ context.Context, id int64) (tmdbfeeds.Detail, error) {
		if id != 42 {
			t.Errorf("detail of %d", id)
		}
		return tmdbfeeds.Detail{Networks: []string{"Example Network"}, Certification: "TV-14"}, nil
	}
	items, more, err := f.Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending}, Page: 2})
	if err != nil || !more {
		t.Fatalf("more = %v, err = %v", more, err)
	}
	want := []media.Recommendation{{
		Provider: "tmdb", ExternalID: "42", Title: "Example Show", Overview: "About it.", PosterURL: "https://image.tmdb.org/t/p/original/p.jpg", Year: 2019,
		ReleasedOn: "2019-03-02", Popularity: 55, Rating: 8.4, Votes: 900, Networks: []string{"Example Network"},
		Traits: media.Traits{Genres: []string{"Drama", "Sci-Fi & Fantasy"}, OriginalLanguage: "ja", OriginCountries: []string{"JP"}, Rating: "TV-14"},
	}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %+v\nwant %+v", items, want)
	}
	if _, more, _ := f.Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending}, Page: 3}); more {
		t.Error("the last page said there is more")
	}
}

func TestAFilmIsAddedByTheMovieKeyAndAFailedChartIsAnError(t *testing.T) {
	source := &charts{answer: tmdb.ListPage{Items: []tmdb.ListItem{{ID: 7, Title: "Example Film", ReleaseDate: "1999-10-15"}}}}
	items, _, err := feeds(source, tmdb.MediaMovie).Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending}, Page: 1})
	if err != nil || items[0].Provider != "tmdb:movie" || items[0].Year != 1999 {
		t.Errorf("item = %+v, %v", items, err)
	}
	boom := errors.New("boom")
	if _, _, err := feeds(&charts{err: boom}, tmdb.MediaMovie).Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending}, Page: 1}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
