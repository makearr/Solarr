// Package olfeeds reads Open Library's charts as the recommendation feeds of book libraries.
package olfeeds

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Charts is the part of the Open Library client the feeds read.
type Charts interface {
	Trending(ctx context.Context, page int) ([]openlibrary.Doc, bool, error)
	RatedBefore(ctx context.Context, year int, minRatings int64, page int) ([]openlibrary.Doc, bool, error)
}

// UpcomingNote is what the unusable upcoming list says: Open Library keeps no release calendar.
const UpcomingNote = "Open Library has no release calendar"

// Readers a rated feed asks of what it ranks, so a few ratings cannot top a chart.
const (
	ratedReaders   = 100
	classicReaders = 300
	// maxSubjects bounds the genres taken from a work's subjects, which Open Library lists by the dozen.
	maxSubjects = 8
	// chartDepth makes a chart's own order the popularity: earlier pages rank above later ones.
	chartDepth = 100000
)

// Feeds recommends books from Open Library.
type Feeds struct {
	Charts Charts
	// Kind is the add kind of the library's format, Provider the key it adds by.
	Kind, Provider string
	Clock          clock.Clock
}

var _ media.Recommending = Feeds{}

// RecommendationFeeds implements media.Recommending; Open Library needs no key. Only the rated lists carry ratings.
func (f Feeds) RecommendationFeeds(context.Context) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feed := func(listType media.RecommendationType, rated bool) media.RecommendationFeed {
		return media.RecommendationFeed{Type: listType, Kind: f.Kind, Source: "Open Library", HasRating: rated}
	}
	return []media.RecommendationFeed{
			feed(media.RecommendTrending, false), feed(media.RecommendHighestRated, true), feed(media.RecommendClassics, true),
		},
		map[media.RecommendationFeed]string{feed(media.RecommendUpcoming, false): UpcomingNote}
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	var (
		docs []openlibrary.Doc
		more bool
		err  error
	)
	switch query.Feed.Type {
	case media.RecommendTrending:
		docs, more, err = f.Charts.Trending(ctx, query.Page)
	case media.RecommendHighestRated:
		docs, more, err = f.Charts.RatedBefore(ctx, 0, ratedReaders, query.Page)
	case media.RecommendClassics:
		docs, more, err = f.Charts.RatedBefore(ctx, f.Clock.Now().Year()-query.ClassicAgeYears, classicReaders, query.Page)
	default:
		return nil, false, fmt.Errorf("openlibrary: no feed %q: %w", query.Feed.Type, media.ErrNotWired)
	}
	if err != nil {
		return nil, false, fmt.Errorf("openlibrary %s: %w", query.Feed.Type, err)
	}
	items := make([]media.Recommendation, 0, len(docs))
	for index, doc := range docs {
		if doc.WorkID() == "" || doc.Title == "" {
			continue
		}
		item := media.Recommendation{
			Kind: f.Kind, Provider: f.Provider, ExternalID: doc.WorkID(), Title: doc.Title, Subtitle: strings.Join(doc.AuthorNames, ", "),
			PosterURL: openlibrary.CoverURL(doc.CoverID), Year: int64(doc.FirstPublishYear), Rating: doc.RatingsAverage * 2, Votes: doc.RatingsCount,
			Popularity: float64(chartDepth - (query.Page-1)*openlibrary.ChartPageSize - index), Traits: media.Traits{Genres: doc.Subjects[:min(len(doc.Subjects), maxSubjects)]},
		}
		if query.Feed.Type != media.RecommendTrending {
			item.Popularity = float64(doc.RatingsCount)
		}
		items = append(items, item)
	}
	return items, more, nil
}
