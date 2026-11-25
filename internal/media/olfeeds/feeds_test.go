package olfeeds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/olfeeds"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type charts struct {
	docs  []openlibrary.Doc
	years []int
	min   []int64
	err   error
}

func (c *charts) Trending(context.Context, int) ([]openlibrary.Doc, bool, error) {
	return c.docs, true, c.err
}
func (c *charts) RatedBefore(_ context.Context, year int, minRatings int64, _ int) ([]openlibrary.Doc, bool, error) {
	c.years, c.min = append(c.years, year), append(c.min, minRatings)
	return c.docs, false, c.err
}

func feeds(c *charts) olfeeds.Feeds {
	return olfeeds.Feeds{Charts: c, Kind: "book", Provider: "openlibrary", Clock: clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))}
}

func ask(listType media.RecommendationType, page int) media.RecommendQuery {
	return media.RecommendQuery{Feed: media.RecommendationFeed{Type: listType, Kind: "book"}, Page: page, ClassicAgeYears: 25}
}

func TestOnlyTheRatedListsClaimRatingsAndUpcomingSaysWhyNot(t *testing.T) {
	usable, unavailable := feeds(&charts{}).RecommendationFeeds(t.Context(), 1)
	rated := map[media.RecommendationType]bool{}
	for _, one := range usable {
		rated[one.Type] = one.HasRating
	}
	want := map[media.RecommendationType]bool{media.RecommendTrending: false, media.RecommendHighestRated: true, media.RecommendClassics: true}
	if !reflect.DeepEqual(rated, want) {
		t.Errorf("usable %v, want %v", rated, want)
	}
	for _, reason := range unavailable {
		if reason != olfeeds.UpcomingNote {
			t.Errorf("reason %q", reason)
		}
	}
}

func TestABookBecomesARecommendationRatedOutOfTenAndTrendingKeepsChartOrder(t *testing.T) {
	c := &charts{docs: []openlibrary.Doc{
		{Key: "/works/OL1W", Title: "First", AuthorNames: []string{"Ada Quill", "Bo Rend"}, FirstPublishYear: 1990, RatingsAverage: 4.25, RatingsCount: 800, Subjects: []string{"Folk tales"}},
		{Key: "/works/OL2W", Title: "Second"},
		{Key: "", Title: "No id"},
	}}
	got, more, err := feeds(c).Recommend(t.Context(), ask(media.RecommendTrending, 1))
	if err != nil || !more || len(got) != 2 {
		t.Fatalf("got %+v more %v err %v", got, more, err)
	}
	first := got[0]
	if first.ExternalID != "OL1W" || first.Provider != "openlibrary" || first.Subtitle != "Ada Quill, Bo Rend" || first.Rating != 8.5 || first.Votes != 800 || first.Traits.Genres[0] != "Folk tales" {
		t.Errorf("first = %+v", first)
	}
	if first.Popularity <= got[1].Popularity {
		t.Errorf("chart order lost: %v <= %v", first.Popularity, got[1].Popularity)
	}
	later, _, _ := feeds(c).Recommend(t.Context(), ask(media.RecommendTrending, 2))
	if later[0].Popularity >= got[1].Popularity {
		t.Errorf("page 2 ranks above page 1: %v >= %v", later[0].Popularity, got[1].Popularity)
	}
}

func TestClassicsAreRatedWorksOlderThanTheLibrarysAgeAndRankedByReaders(t *testing.T) {
	c := &charts{docs: []openlibrary.Doc{{Key: "/works/OL1W", Title: "Old", RatingsCount: 900}}}
	got, _, err := feeds(c).Recommend(t.Context(), ask(media.RecommendClassics, 1))
	if err != nil || got[0].Popularity != 900 || !reflect.DeepEqual(c.years, []int{2001}) {
		t.Errorf("got %+v years %v err %v", got, c.years, err)
	}
	if _, _, err := feeds(c).Recommend(t.Context(), ask(media.RecommendHighestRated, 1)); err != nil || c.years[1] != 0 {
		t.Errorf("highest rated years %v err %v", c.years, err)
	}
}

func TestAFailureNamesTheListAndUpcomingIsNotWired(t *testing.T) {
	boom := errors.New("down")
	if _, _, err := feeds(&charts{err: boom}).Recommend(t.Context(), ask(media.RecommendTrending, 1)); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if _, _, err := feeds(&charts{}).Recommend(t.Context(), ask(media.RecommendUpcoming, 1)); !errors.Is(err, media.ErrNotWired) {
		t.Errorf("upcoming err = %v", err)
	}
}
