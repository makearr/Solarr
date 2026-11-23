// Package audiblefeeds reads Audible's catalogue orderings as the recommendation feeds of audiobook libraries.
package audiblefeeds

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Charts is the part of the Audible client the feeds read.
type Charts interface {
	Chart(ctx context.Context, sort audible.ChartSort, page int) ([]audible.Product, bool, error)
}

// ClassicsNote is what the unusable classics list says: the catalogue cannot be cut by release date.
const ClassicsNote = "Audible's catalogue cannot be filtered by release date"

// chartDepth makes a chart's own order the popularity: earlier pages rank above later ones.
const chartDepth = 100000

// languages are the codes Audible's language names stand for, the way the library's language rule spells them.
var languages = map[string]string{
	"english": "en", "german": "de", "french": "fr", "spanish": "es", "italian": "it", "japanese": "ja", "portuguese": "pt", "dutch": "nl",
}

// Feeds recommends audiobooks from Audible.
type Feeds struct {
	Charts Charts
	// Kind is the add kind of the library's format.
	Kind  string
	Clock clock.Clock
}

var _ media.Recommending = Feeds{}

// RecommendationFeeds implements media.Recommending; Audible's catalogue needs no key. Only the rated list carries ratings.
func (f Feeds) RecommendationFeeds(context.Context) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feed := func(listType media.RecommendationType, rated bool) media.RecommendationFeed {
		return media.RecommendationFeed{Type: listType, Kind: f.Kind, Source: "Audible", HasRating: rated}
	}
	return []media.RecommendationFeed{
			feed(media.RecommendTrending, false), feed(media.RecommendHighestRated, true), feed(media.RecommendUpcoming, false),
		},
		map[media.RecommendationFeed]string{feed(media.RecommendClassics, true): ClassicsNote}
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	sort := audible.SortBestSellers
	switch query.Feed.Type {
	case media.RecommendTrending:
	case media.RecommendHighestRated:
		sort = audible.SortAverageRating
	case media.RecommendUpcoming:
		sort = audible.SortNewestFirst
	default:
		return nil, false, fmt.Errorf("audible: no feed %q: %w", query.Feed.Type, media.ErrNotWired)
	}
	products, more, err := f.Charts.Chart(ctx, sort, query.Page)
	if err != nil {
		return nil, false, fmt.Errorf("audible %s: %w", query.Feed.Type, err)
	}
	today := f.Clock.Now().Format("2006-01-02")
	items := make([]media.Recommendation, 0, len(products))
	for index, product := range products {
		if product.ASIN == "" || product.Title == "" {
			continue
		}
		// The newest-first ordering starts in the future and runs on into the past; only what has not come out is upcoming.
		if query.Feed.Type == media.RecommendUpcoming && product.ReleaseDate <= today {
			more = false
			continue
		}
		authors := make([]string, 0, len(product.Authors))
		for _, author := range product.Authors {
			authors = append(authors, author.Name)
		}
		item := media.Recommendation{
			Kind: f.Kind, Provider: "audible", ExternalID: strings.ToUpper(product.ASIN), Title: product.Title, Subtitle: strings.Join(authors, ", "),
			Overview: product.Summary, PosterURL: product.Cover(), Year: int64(product.Year()), ReleasedOn: product.ReleaseDate,
			Popularity: float64(chartDepth - (query.Page-1)*audible.SearchPageSize - index),
			Rating:     product.Rating.Distribution.Average * 2, Votes: product.Rating.Distribution.Count,
			Traits: media.Traits{OriginalLanguage: languages[strings.ToLower(product.Language)]},
		}
		items = append(items, item)
	}
	return items, more, nil
}
