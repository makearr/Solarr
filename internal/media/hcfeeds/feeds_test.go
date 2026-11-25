package hcfeeds_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/hcfeeds"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type charts struct {
	configured bool
	books      []hardcover.ChartBook
	asked      []string
}

func (c *charts) Configured() bool { return c.configured }
func (c *charts) Trending(_ context.Context, from, to string, _, _ int) ([]hardcover.ChartBook, error) {
	c.asked = append(c.asked, from+".."+to)
	return c.books, nil
}
func (c *charts) TopRated(context.Context, int64, int, int, int) ([]hardcover.ChartBook, error) {
	return c.books, nil
}
func (c *charts) Upcoming(_ context.Context, after string, _, _ int) ([]hardcover.ChartBook, error) {
	c.asked = append(c.asked, after)
	return c.books, nil
}

func book(t *testing.T, raw string) hardcover.ChartBook {
	t.Helper()
	var one hardcover.ChartBook
	if err := json.Unmarshal([]byte(raw), &one); err != nil {
		t.Fatal(err)
	}
	return one
}

func TestTrendingKeysABookByItsOpenLibraryWorkWhereAnISBNNamesOne(t *testing.T) {
	c := &charts{configured: true, books: []hardcover.ChartBook{
		book(t, `{"id":11,"title":"Known Book","release_year":2020,"rating":4.5,"ratings_count":700,"users_count":900,`+
			`"contributions":[{"author":{"name":"Example Author"}}],"default_physical_edition":{"isbn_13":"9780000000001"}}`),
		book(t, `{"id":12,"title":"Other Book","rating":3,"ratings_count":10,"users_count":50}`),
	}}
	f := hcfeeds.Feeds{
		Charts: c, Kind: "book", WorkProvider: "openlibrary", HardcoverProvider: "hardcover", Clock: clock.NewFake(time.Date(2030, 3, 31, 0, 0, 0, 0, time.UTC)),
		Resolve: func(_ context.Context, isbn string) (string, bool, error) {
			return "OL1W", isbn == "9780000000001", nil
		},
	}
	items, more, err := f.Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending, Kind: "book"}, Page: 1})
	want := []media.Recommendation{
		{Kind: "book", Provider: "openlibrary", ExternalID: "OL1W", Title: "Known Book", Subtitle: "Example Author", Year: 2020, Rating: 9, Votes: 700, Popularity: 100000},
		{Kind: "book", Provider: "hardcover", ExternalID: "12", Title: "Other Book", Rating: 6, Votes: 10, Popularity: 99999},
	}
	if err != nil || more || !reflect.DeepEqual(items, want) {
		t.Errorf("items %+v more %v err %v", items, more, err)
	}
	if want := []string{"2030-03-01..2030-03-31"}; !reflect.DeepEqual(c.asked, want) {
		t.Errorf("asked %v, want %v", c.asked, want)
	}
}

func TestNoTokenMakesEveryFeedUnavailable(t *testing.T) {
	usable, unavailable := hcfeeds.Feeds{Charts: &charts{}, Kind: "book"}.RecommendationFeeds(t.Context(), 1)
	if len(usable) != 0 || len(unavailable) != 4 {
		t.Errorf("usable %v unavailable %v", usable, unavailable)
	}
}
