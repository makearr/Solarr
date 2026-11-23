package adultfeeds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/adultfeeds"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type charts struct {
	configured bool
	scenes     []stashbox.Scene
	days       []string
	trending   int
	err        error
}

func (c *charts) Configured() bool { return c.configured }
func (c *charts) Trending(context.Context, int) ([]stashbox.Scene, error) {
	c.trending++
	return c.scenes, c.err
}
func (c *charts) ReleasedAfter(_ context.Context, day string, _ int) ([]stashbox.Scene, error) {
	c.days = append(c.days, day)
	return c.scenes, c.err
}

func feeds(c *charts) adultfeeds.Feeds {
	return adultfeeds.Feeds{Charts: c, Kind: "adult", Provider: "stashdb", Source: "StashDB", Clock: clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))}
}

func ask(listType media.RecommendationType, page int) media.RecommendQuery {
	return media.RecommendQuery{Feed: media.RecommendationFeed{Type: listType, Kind: "adult"}, Page: page}
}

func TestWithoutTheKeyEveryFeedSaysWhyAndWithItOnlyRatingsDo(t *testing.T) {
	usable, unavailable := feeds(&charts{}).RecommendationFeeds(t.Context())
	if len(usable) != 0 || len(unavailable) != 4 {
		t.Errorf("without key: usable %v unavailable %v", usable, unavailable)
	}
	usable, unavailable = feeds(&charts{configured: true}).RecommendationFeeds(t.Context())
	got := map[media.RecommendationType]bool{}
	for _, one := range usable {
		got[one.Type] = one.HasRating
	}
	if want := map[media.RecommendationType]bool{media.RecommendTrending: false, media.RecommendUpcoming: false}; !reflect.DeepEqual(got, want) {
		t.Errorf("usable %v", got)
	}
	for feed, reason := range unavailable {
		if (feed.Type != media.RecommendHighestRated && feed.Type != media.RecommendClassics) || reason != adultfeeds.NoRatesNote {
			t.Errorf("unavailable %v: %q", feed, reason)
		}
	}
}

func TestASceneBecomesARecommendationWithStudioPerformersAndTags(t *testing.T) {
	c := &charts{configured: true, scenes: []stashbox.Scene{
		{
			ID: "s1", Title: "Night Class", Details: "A story.", ReleaseDate: "2026-05-30", Studio: &stashbox.Studio{Name: "Studio One"},
			Images:     []stashbox.Image{{URL: "small", Width: 10, Height: 10}, {URL: "large", Width: 100, Height: 50}},
			Performers: []stashbox.Appearance{{Performer: stashbox.Performer{Name: "Ada Vale"}}, {Performer: stashbox.Performer{Name: "Bo Rend"}}},
			Tags:       []stashbox.Tag{{Name: "Classroom"}},
		},
		{ID: "", Title: "No id"},
	}}
	got, more, err := feeds(c).Recommend(t.Context(), ask(media.RecommendTrending, 2))
	if err != nil || more || len(got) != 1 {
		t.Fatalf("got %+v more %v, %v", got, more, err)
	}
	want := media.Recommendation{
		Kind: "adult", Provider: "stashdb", ExternalID: "s1", Title: "Night Class", Overview: "A story.", PosterURL: "large", Year: 2026, ReleasedOn: "2026-05-30",
		Subtitle: "Studio One - Ada Vale, Bo Rend", Networks: []string{"Studio One"}, Popularity: 100000 - stashbox.ChartPageSize, Traits: media.Traits{Genres: []string{"Classroom"}},
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestUpcomingAsksForScenesAfterTodayAndAFullPageHasMore(t *testing.T) {
	c := &charts{configured: true, scenes: make([]stashbox.Scene, stashbox.ChartPageSize)}
	_, more, err := feeds(c).Recommend(t.Context(), ask(media.RecommendUpcoming, 1))
	if err != nil || !more || !reflect.DeepEqual(c.days, []string{"2026-06-01"}) || c.trending != 0 {
		t.Errorf("more %v days %v trending %d, %v", more, c.days, c.trending, err)
	}
}

func TestARatingFeedAndAFailureAreErrors(t *testing.T) {
	if _, _, err := feeds(&charts{}).Recommend(t.Context(), ask(media.RecommendHighestRated, 1)); !errors.Is(err, media.ErrNotWired) {
		t.Errorf("rated error %v", err)
	}
	boom := errors.New("down")
	if _, _, err := feeds(&charts{err: boom}).Recommend(t.Context(), ask(media.RecommendTrending, 1)); !errors.Is(err, boom) {
		t.Errorf("error %v", err)
	}
}
