package audiblefeeds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/audiblefeeds"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type charts struct {
	products []audible.Product
	more     bool
	sorts    []audible.ChartSort
	err      error
}

func (c *charts) Chart(_ context.Context, sort audible.ChartSort, _ int) ([]audible.Product, bool, error) {
	c.sorts = append(c.sorts, sort)
	return c.products, c.more, c.err
}

func feeds(c *charts) audiblefeeds.Feeds {
	return audiblefeeds.Feeds{Charts: c, Kind: "audiobook", Clock: clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))}
}

func ask(listType media.RecommendationType, page int) media.RecommendQuery {
	return media.RecommendQuery{Feed: media.RecommendationFeed{Type: listType, Kind: "audiobook"}, Page: page}
}

func product(asin, title, released string) audible.Product {
	return audible.Product{ASIN: asin, Title: title, ReleaseDate: released, Language: "German", Authors: []audible.Person{{Name: "Ada Quill"}, {Name: "Bo Rend"}}}
}

func TestClassicsSaysWhyNotAndOnlyTheRatedListClaimsRatings(t *testing.T) {
	usable, unavailable := feeds(&charts{}).RecommendationFeeds(t.Context())
	rated := map[media.RecommendationType]bool{}
	for _, one := range usable {
		rated[one.Type] = one.HasRating
	}
	want := map[media.RecommendationType]bool{media.RecommendTrending: false, media.RecommendHighestRated: true, media.RecommendUpcoming: false}
	if !reflect.DeepEqual(rated, want) {
		t.Errorf("usable %v, want %v", rated, want)
	}
	for feed, reason := range unavailable {
		if feed.Type != media.RecommendClassics || reason != audiblefeeds.ClassicsNote {
			t.Errorf("unavailable %v: %q", feed, reason)
		}
	}
}

func TestAProductBecomesARecommendationRatedOutOfTenInChartOrder(t *testing.T) {
	first := product("b000000001", "First", "2020-03-02")
	first.Rating.Distribution.Average, first.Rating.Distribution.Count = 4.5, 300
	c := &charts{products: []audible.Product{first, product("B000000002", "Second", ""), product("", "No id", "")}, more: true}
	got, more, err := feeds(c).Recommend(t.Context(), ask(media.RecommendHighestRated, 2))
	if err != nil || !more || len(got) != 2 {
		t.Fatalf("got %+v more %v, %v", got, more, err)
	}
	want := media.Recommendation{
		Kind: "audiobook", Provider: "audible", ExternalID: "B000000001", Title: "First", Subtitle: "Ada Quill, Bo Rend", Year: 2020, ReleasedOn: "2020-03-02",
		Popularity: 100000 - audible.SearchPageSize, Rating: 9, Votes: 300, Traits: media.Traits{OriginalLanguage: "de"},
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("first = %+v, want %+v", got[0], want)
	}
	if got[1].Popularity != want.Popularity-1 || !reflect.DeepEqual(c.sorts, []audible.ChartSort{audible.SortAverageRating}) {
		t.Errorf("second popularity %v, sorts %v", got[1].Popularity, c.sorts)
	}
}

func TestUpcomingKeepsOnlyWhatIsNotOutYetAndStopsAtTheFirstReleasedOne(t *testing.T) {
	c := &charts{products: []audible.Product{product("B000000001", "Soon", "2026-09-01"), product("B000000002", "Out", "2026-05-01")}, more: true}
	got, more, err := feeds(c).Recommend(t.Context(), ask(media.RecommendUpcoming, 1))
	if err != nil || more || len(got) != 1 || got[0].Title != "Soon" || c.sorts[0] != audible.SortNewestFirst {
		t.Errorf("got %+v more %v sorts %v, %v", got, more, c.sorts, err)
	}
}

func TestAFeedTheCatalogueCannotServeAndAFailureAreErrors(t *testing.T) {
	if _, _, err := feeds(&charts{}).Recommend(t.Context(), ask(media.RecommendClassics, 1)); !errors.Is(err, media.ErrNotWired) {
		t.Errorf("classics error %v", err)
	}
	boom := errors.New("down")
	if _, _, err := feeds(&charts{err: boom}).Recommend(t.Context(), ask(media.RecommendTrending, 1)); !errors.Is(err, boom) {
		t.Errorf("error %v", err)
	}
}
