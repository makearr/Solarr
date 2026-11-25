// Package anifeeds reads AniList's charts as the recommendation feeds of anime libraries.
package anifeeds

import (
	"context"
	"fmt"
	"strings"

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

// providerAniList is the provider key of an AniList id in a list entry.
const providerAniList = "anilist"

// pageSize keeps the lookups one page costs small: every entry is resolved to the series the library adds it by.
const pageSize = 20

// minPopularity is the number of lists an anime must be on to rank by score.
const minPopularity = 20000

// Feeds recommends anime from AniList; the library's type names the series each entry is.
type Feeds struct {
	Charts Charts
	// Resolve names the series an AniList entry is, by an id the type adds by.
	Resolve func(ctx context.Context, entry media.ListEntry) (media.ListMatch, error)
	Clock   clock.Clock
}

var _ media.Recommending = Feeds{}

var feedTypes = []media.RecommendationType{media.RecommendTrending, media.RecommendHighestRated, media.RecommendUpcoming, media.RecommendClassics}

// RecommendationFeeds implements media.Recommending; AniList needs no key.
func (Feeds) RecommendationFeeds(context.Context, int64) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feeds := make([]media.RecommendationFeed, len(feedTypes))
	for index, one := range feedTypes {
		feeds[index] = media.RecommendationFeed{Type: one, Source: "AniList", SourceKey: sourceKey, HasRating: true}
	}
	return feeds, nil
}

// Recommend implements media.Recommending; an entry that is no series, or that no series is found for, is left out.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	chart, err := f.chart(query)
	if err != nil {
		return nil, false, err
	}
	entries, more, err := f.Charts.Chart(ctx, chart, query.Page)
	if err != nil {
		return nil, false, fmt.Errorf("anilist %s: %w", query.Feed.Type, err)
	}
	items := make([]media.Recommendation, 0, len(entries))
	for _, entry := range entries {
		listed := listEntry(entry)
		match, err := f.Resolve(ctx, listed)
		if err != nil {
			return nil, false, err
		}
		if !match.Found {
			continue
		}
		items = append(items, media.Recommendation{
			Provider: match.Provider, ExternalID: match.ExternalID, Title: title(entry), Overview: entry.Description, PosterURL: entry.CoverImage.ExtraLarge,
			Year: int64(entry.SeasonYear), Popularity: float64(entry.Popularity), Rating: float64(entry.AverageScore) / 10, Votes: int64(entry.Popularity),
			Traits: media.Traits{Genres: entry.Genres, OriginalLanguage: "ja", OriginCountries: []string{"JP"}, AniList: true},
		})
	}
	return items, more, nil
}

func (f Feeds) chart(query media.RecommendQuery) (anilist.ChartQuery, error) {
	chart := anilist.ChartQuery{ExcludeGenres: query.ExcludeGenres, PerPage: pageSize}
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
		return anilist.ChartQuery{}, fmt.Errorf("no such feed %q", query.Feed.Type)
	}
	return chart, nil
}

func title(entry anilist.Media) string {
	if entry.Title.English != "" {
		return entry.Title.English
	}
	return entry.Title.Romaji
}

func listEntry(entry anilist.Media) media.ListEntry {
	var alternatives []string
	for _, name := range entry.Titles() {
		if !strings.EqualFold(name, title(entry)) {
			alternatives = append(alternatives, name)
		}
	}
	return media.ListEntry{
		Title: title(entry), Year: entry.SeasonYear, Form: entry.Format, AltTitles: alternatives,
		IDs: []media.ProviderID{{Provider: providerAniList, ExternalID: fmt.Sprint(entry.ID)}},
	}
}
