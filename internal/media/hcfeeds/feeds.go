// Package hcfeeds reads Hardcover's charts as recommendation feeds of book libraries.
package hcfeeds

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// sourceKey is the provider's key in the metadata settings, which switch the feeds off with the provider.
const sourceKey = "hardcover"

// Charts is the part of the Hardcover client the feeds read.
type Charts interface {
	Configured() bool
	Trending(ctx context.Context, from, to string, limit, offset int) ([]hardcover.ChartBook, error)
	TopRated(ctx context.Context, minRatings int64, releasedBy, limit, offset int) ([]hardcover.ChartBook, error)
	Upcoming(ctx context.Context, after string, limit, offset int) ([]hardcover.ChartBook, error)
}

// NoKeyReason is what every feed says while the owner has stored no Hardcover token.
const NoKeyReason = "needs the Hardcover token, under Settings, Metadata"

const (
	pageSize = 20
	// trendingDays is the window the trending chart counts reads over.
	trendingDays = 30
	// Ratings a rated feed asks of what it ranks, so a few ratings cannot top a chart.
	ratedRatings, classicRatings = 500, 300
	// chartDepth makes the trending chart's own order the popularity: earlier pages rank above later ones.
	chartDepth = 100000
)

// Feeds recommends books from Hardcover; an entry is keyed by its Open Library work where Resolve finds one, so Open Library's
// lists naming the same book name the same item, else by its Hardcover id.
type Feeds struct {
	Charts Charts
	// Kind is the add kind of the library's format; WorkProvider and HardcoverProvider the keys it adds an Open Library work and a Hardcover book by.
	Kind, WorkProvider, HardcoverProvider string
	// Resolve names the Open Library work of an ISBN; nil keys every entry by its Hardcover id.
	Resolve func(ctx context.Context, isbn string) (workID string, found bool, err error)
	Clock   clock.Clock
}

var _ media.Recommending = Feeds{}

var feedTypes = []media.RecommendationType{media.RecommendTrending, media.RecommendHighestRated, media.RecommendUpcoming, media.RecommendClassics}

// RecommendationFeeds implements media.Recommending; Hardcover answers only with the owner's token.
func (f Feeds) RecommendationFeeds(context.Context, int64) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feeds := make([]media.RecommendationFeed, len(feedTypes))
	for index, one := range feedTypes {
		feeds[index] = media.RecommendationFeed{Type: one, Kind: f.Kind, Source: "Hardcover", SourceKey: sourceKey, HasRating: true}
	}
	if !f.Charts.Configured() {
		unavailable := map[media.RecommendationFeed]string{}
		for _, feed := range feeds {
			unavailable[feed] = NoKeyReason
		}
		return nil, unavailable
	}
	return feeds, nil
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	const day = "2006-01-02"
	today, offset := f.Clock.Now(), (query.Page-1)*pageSize
	var (
		books []hardcover.ChartBook
		err   error
	)
	switch query.Feed.Type {
	case media.RecommendTrending:
		books, err = f.Charts.Trending(ctx, today.AddDate(0, 0, -trendingDays).Format(day), today.Format(day), pageSize, offset)
	case media.RecommendHighestRated:
		books, err = f.Charts.TopRated(ctx, ratedRatings, 0, pageSize, offset)
	case media.RecommendUpcoming:
		books, err = f.Charts.Upcoming(ctx, today.Format(day), pageSize, offset)
	case media.RecommendClassics:
		books, err = f.Charts.TopRated(ctx, classicRatings, today.Year()-query.ClassicAgeYears, pageSize, offset)
	default:
		return nil, false, fmt.Errorf("hardcover: no feed %q: %w", query.Feed.Type, media.ErrNotWired)
	}
	if errors.Is(err, hardcover.ErrNoKey) {
		return nil, false, fmt.Errorf("hardcover %s: %w", query.Feed.Type, media.ErrSourceUnavailable)
	}
	if err != nil {
		return nil, false, fmt.Errorf("hardcover %s: %w", query.Feed.Type, err)
	}
	items := make([]media.Recommendation, 0, len(books))
	for index, book := range books {
		item := f.recommendation(ctx, book)
		if query.Feed.Type == media.RecommendTrending {
			item.Popularity = float64(chartDepth - offset - index)
		}
		items = append(items, item)
	}
	return items, len(books) == pageSize, nil
}

func (f Feeds) recommendation(ctx context.Context, book hardcover.ChartBook) media.Recommendation {
	item := media.Recommendation{
		Kind: f.Kind, Provider: f.HardcoverProvider, ExternalID: strconv.FormatInt(book.ID, 10), Title: book.Title, Subtitle: strings.Join(book.Authors(), ", "),
		PosterURL: book.Image.URL, Year: int64(book.ReleaseYear), ReleasedOn: book.ReleaseDate, Rating: book.Rating * 2, Votes: book.RatingsCount,
		Popularity: float64(book.UsersCount),
	}
	if f.Resolve == nil {
		return item
	}
	for _, isbn := range book.ISBNs() {
		// A failed lookup keeps the Hardcover id: the book stays addable, it only may not merge with Open Library's entry.
		if workID, found, err := f.Resolve(ctx, isbn); err == nil && found {
			item.Provider, item.ExternalID = f.WorkProvider, workID
			break
		}
	}
	return item
}
