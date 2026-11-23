package anifeeds_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/anifeeds"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type charts struct {
	asked   []anilist.ChartQuery
	entries []anilist.Media
}

func (c *charts) Chart(_ context.Context, chart anilist.ChartQuery, _ int) ([]anilist.Media, bool, error) {
	c.asked = append(c.asked, chart)
	return c.entries, true, nil
}

func entry(id int64, english, format string) anilist.Media {
	var m anilist.Media
	m.ID, m.Format, m.AverageScore, m.Popularity, m.SeasonYear, m.Genres = id, format, 84, 30000, 2024, []string{"Drama"}
	m.Title.English, m.Title.Romaji = english, "Romaji "+english
	return m
}

func resolver(_ context.Context, listed media.ListEntry) (media.ListMatch, error) {
	switch listed.IDs[0].ExternalID {
	case "1":
		return media.ListMatch{ProviderID: media.ProviderID{Provider: "tvdb", ExternalID: "900"}, Found: true}, nil
	case "2":
		return media.ListMatch{Foreign: true}, nil
	}
	return media.ListMatch{}, nil
}

func TestRecommendKeepsOnlyEntriesASeriesWasFoundFor(t *testing.T) {
	c := &charts{entries: []anilist.Media{entry(1, "Example Ray", "TV"), entry(2, "Example Film", "MOVIE"), entry(3, "Unknown", "TV")}}
	feeds := anifeeds.Feeds{Charts: c, Resolve: resolver, Clock: clock.NewFake(time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC))}
	items, more, err := feeds.Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendClassics}, Page: 1, ClassicAgeYears: 25, ExcludeGenres: []string{"Ecchi"}})
	if err != nil || !more {
		t.Fatalf("err = %v, more = %v", err, more)
	}
	want := []media.Recommendation{{
		Provider: "tvdb", ExternalID: "900", Title: "Example Ray", Year: 2024, Popularity: 30000, Votes: 30000, Rating: 8.4,
		Traits: media.Traits{Genres: []string{"Drama"}, OriginalLanguage: "ja", OriginCountries: []string{"JP"}, AniList: true},
	}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %+v, want %+v", items, want)
	}
	wantChart := anilist.ChartQuery{Sort: anilist.SortScore, MinPopularity: 20000, StartedBefore: 20010605, ExcludeGenres: []string{"Ecchi"}, PerPage: 20}
	if !reflect.DeepEqual(c.asked, []anilist.ChartQuery{wantChart}) {
		t.Errorf("chart = %+v, want %+v", c.asked, wantChart)
	}
}

func TestEveryFeedTypeMapsToAChartAndUpcomingAsksUnreleased(t *testing.T) {
	c := &charts{}
	feeds := anifeeds.Feeds{Charts: c, Resolve: resolver, Clock: clock.NewFake(time.Unix(0, 0))}
	usable, unavailable := feeds.RecommendationFeeds(t.Context())
	if len(usable) != 4 || len(unavailable) != 0 {
		t.Fatalf("usable = %v, unavailable = %v", usable, unavailable)
	}
	for _, feed := range usable {
		if _, _, err := feeds.Recommend(t.Context(), media.RecommendQuery{Feed: feed, Page: 1}); err != nil {
			t.Fatal(err)
		}
	}
	sorts := []string{c.asked[0].Sort, c.asked[1].Sort, c.asked[2].Sort, c.asked[3].Sort}
	if want := []string{"TRENDING_DESC", "SCORE_DESC", "POPULARITY_DESC", "SCORE_DESC"}; !reflect.DeepEqual(sorts, want) || c.asked[2].Status != anilist.StatusNotYetReleased {
		t.Errorf("sorts = %v, asked = %+v", sorts, c.asked)
	}
}
