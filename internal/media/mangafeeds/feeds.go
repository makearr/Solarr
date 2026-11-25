// Package mangafeeds reads AniList's manga charts as the recommendation feeds of comic libraries.
package mangafeeds

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// sourceKey is the provider's key in the metadata settings, which switch the feeds off with the provider.
const sourceKey = "anilist"

// Charts is the part of the AniList client the feeds read.
type Charts interface {
	Chart(ctx context.Context, chart anilist.ChartQuery, page int) ([]anilist.Media, bool, error)
}

const (
	pageSize = 20
	// minPopularity is the number of lists a manga must be on to rank by score.
	minPopularity = 5000
)

// WesternNote says what the feeds leave out: AniList knows manga, not Western comics.
const WesternNote = "AniList covers manga; Western comics have no free popularity chart"

// Feeds recommends manga from AniList, added by the id AniList gives it.
type Feeds struct {
	Charts Charts
	// Kind is the add kind of the library's type, Provider the key it adds an AniList manga by.
	Kind, Provider string
	Clock          clock.Clock
}

var _ media.Recommending = Feeds{}

var feedTypes = []media.RecommendationType{media.RecommendTrending, media.RecommendHighestRated, media.RecommendUpcoming, media.RecommendClassics}

// RecommendationFeeds implements media.Recommending; AniList needs no key.
func (f Feeds) RecommendationFeeds(context.Context, int64) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feeds := make([]media.RecommendationFeed, len(feedTypes))
	for index, one := range feedTypes {
		feeds[index] = media.RecommendationFeed{Type: one, Kind: f.Kind, Source: "AniList", SourceKey: sourceKey, HasRating: true}
	}
	return feeds, nil
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	chart := anilist.ChartQuery{Manga: true, ExcludeGenres: query.ExcludeGenres, PerPage: pageSize}
	switch query.Feed.Type {
	case media.RecommendTrending:
		chart.Sort = anilist.SortTrending
	case media.RecommendHighestRated:
		chart.Sort, chart.MinPopularity = anilist.SortScore, minPopularity
	case media.RecommendUpcoming:
		chart.Sort, chart.Status = anilist.SortPopularity, anilist.StatusNotYetReleased
	case media.RecommendClassics:
		before := f.Clock.Now().AddDate(-query.ClassicAgeYears, 0, 0)
		chart.Sort, chart.MinPopularity = anilist.SortScore, minPopularity
		chart.StartedBefore = before.Year()*10000 + int(before.Month())*100 + before.Day()
	default:
		return nil, false, fmt.Errorf("anilist: no manga feed %q: %w", query.Feed.Type, media.ErrNotWired)
	}
	entries, more, err := f.Charts.Chart(ctx, chart, query.Page)
	if err != nil {
		return nil, false, fmt.Errorf("anilist manga %s: %w", query.Feed.Type, err)
	}
	items := make([]media.Recommendation, 0, len(entries))
	for _, entry := range entries {
		title := entry.Title.English
		if title == "" {
			title = entry.Title.Romaji
		}
		items = append(items, media.Recommendation{
			Kind: f.Kind, Provider: f.Provider, ExternalID: fmt.Sprint(entry.ID), Title: title, Overview: entry.Description, PosterURL: entry.CoverImage.ExtraLarge,
			Year: int64(entry.StartDate.Year), Popularity: float64(entry.Popularity), Rating: float64(entry.AverageScore) / 10, Votes: int64(entry.Popularity),
			Traits: media.Traits{Genres: entry.Genres, OriginalLanguage: "ja", OriginCountries: []string{"JP"}, AniList: true},
		})
	}
	return items, more, nil
}
