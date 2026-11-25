package mangafeeds_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/mangafeeds"
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

func TestEveryListReadsMangaAndClassicsLookBackTheLibrarysAge(t *testing.T) {
	c := &charts{}
	feeds := mangafeeds.Feeds{Charts: c, Kind: "series", Provider: "anilist:manga", Clock: clock.NewFake(time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC))}
	usable, _ := feeds.RecommendationFeeds(t.Context(), 1)
	if len(usable) != 4 || usable[0].Kind != "series" || !usable[0].HasRating {
		t.Fatalf("usable = %+v", usable)
	}
	for _, listType := range []media.RecommendationType{media.RecommendTrending, media.RecommendHighestRated, media.RecommendUpcoming, media.RecommendClassics} {
		if _, _, err := feeds.Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: listType}, Page: 1, ClassicAgeYears: 25}); err != nil {
			t.Fatal(err)
		}
	}
	for _, chart := range c.asked {
		if !chart.Manga {
			t.Errorf("chart %+v does not read manga", chart)
		}
	}
	if c.asked[3].StartedBefore != 20010605 || c.asked[2].Status != anilist.StatusNotYetReleased || c.asked[0].Sort != anilist.SortTrending {
		t.Errorf("charts = %+v", c.asked)
	}
}

func TestAMangaIsAddedByItsAniListIdWithTheYearItStarted(t *testing.T) {
	entry := anilist.Media{ID: 77, AverageScore: 84, Popularity: 9000, Genres: []string{"Drama"}}
	entry.Title.Romaji = "Example Peak"
	entry.StartDate.Year = 1999
	feeds := mangafeeds.Feeds{Charts: &charts{entries: []anilist.Media{entry}}, Kind: "series", Provider: "anilist:manga", Clock: clock.NewFakeAtEpoch()}
	got, more, err := feeds.Recommend(t.Context(), media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending}, Page: 1})
	want := []media.Recommendation{{
		Kind: "series", Provider: "anilist:manga", ExternalID: "77", Title: "Example Peak", Year: 1999, Popularity: 9000, Rating: 8.4, Votes: 9000,
		Traits: media.Traits{Genres: []string{"Drama"}, OriginalLanguage: "ja", OriginCountries: []string{"JP"}, AniList: true},
	}}
	if err != nil || !more || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v more %v err %v", got, more, err)
	}
}
