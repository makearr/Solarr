package media

import (
	"context"
	"fmt"
	"slices"
)

// RecommendationType names one kind of recommendation list.
type RecommendationType string

// The lists Discover offers; collections are built from the library itself, never asked of a type.
const (
	RecommendHighestRated RecommendationType = "highest_rated"
	RecommendTrending     RecommendationType = "trending"
	RecommendUpcoming     RecommendationType = "upcoming"
	RecommendClassics     RecommendationType = "classics"
	RecommendCollections  RecommendationType = "collections"
)

// RecommendationFeed is one list a type can read: a recommendation type for one of its add kinds.
type RecommendationFeed struct {
	Type RecommendationType
	// Kind is the add kind the list's items are added as (an album, a performer); empty for a type of one kind.
	Kind string
	// Source is the provider's display name.
	Source string
	// SourceKey is the provider's settings key: what switches the source off and what keeps its read apart in the pool.
	SourceKey string
	// HasRating is false when the source carries no ratings, which skips the rating gate of auto-add.
	HasRating bool
}

// RecommendQuery asks one page of a feed; the exclusions are hints a source may push down, never relied on.
type RecommendQuery struct {
	CategoryID      int64
	Feed            RecommendationFeed
	Page            int
	ClassicAgeYears int
	Languages       []string
	ExcludeGenres   []string
	// MaxAge is the library's maximum age rating, -1 for no limit.
	MaxAge int64
}

// Recommendation is one thing a feed names, addable by Kind, Provider and ExternalID through the type's AddWork.
type Recommendation struct {
	Kind, Provider, ExternalID, Title string
	Subtitle, Overview, PosterURL     string
	Year                              int64
	// ReleasedOn is an ISO date, empty when unknown.
	ReleasedOn string
	Popularity float64
	// Rating is 0 to 10, 0 when the source has none.
	Rating   float64
	Votes    int64
	Networks []string
	Traits   Traits
}

// Recommending is the capability of a media type whose sources have charts to recommend from.
type Recommending interface {
	// RecommendationFeeds answers a library's feeds usable now and, for each that is not, why (a missing key).
	RecommendationFeeds(ctx context.Context, categoryID int64) (usable []RecommendationFeed, unavailable map[RecommendationFeed]string)
	// Recommend reads one page of a feed; more says whether a next page exists.
	Recommend(ctx context.Context, query RecommendQuery) (items []Recommendation, more bool, err error)
}

// JoinedFeeds is every source of a type as one: their feeds side by side, each page asked of the source that offers the feed.
type JoinedFeeds []Recommending

var _ Recommending = JoinedFeeds{}

// RecommendationFeeds implements Recommending.
func (j JoinedFeeds) RecommendationFeeds(ctx context.Context, categoryID int64) ([]RecommendationFeed, map[RecommendationFeed]string) {
	var usable []RecommendationFeed
	unavailable := map[RecommendationFeed]string{}
	for _, source := range j {
		feeds, reasons := source.RecommendationFeeds(ctx, categoryID)
		usable = append(usable, feeds...)
		for feed, reason := range reasons {
			unavailable[feed] = reason
		}
	}
	return usable, unavailable
}

// Recommend implements Recommending.
func (j JoinedFeeds) Recommend(ctx context.Context, query RecommendQuery) ([]Recommendation, bool, error) {
	for _, source := range j {
		if feeds, _ := source.RecommendationFeeds(ctx, query.CategoryID); slices.Contains(feeds, query.Feed) {
			return source.Recommend(ctx, query)
		}
	}
	return nil, false, fmt.Errorf("no source offers %s of %s from %s: %w", query.Feed.Type, query.Feed.Kind, query.Feed.SourceKey, ErrNotWired)
}
