package media

import "context"

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
	// RecommendationFeeds answers the feeds usable now and, for each that is not, why (a missing key).
	RecommendationFeeds(ctx context.Context) (usable []RecommendationFeed, unavailable map[RecommendationFeed]string)
	// Recommend reads one page of a feed; more says whether a next page exists.
	Recommend(ctx context.Context, query RecommendQuery) (items []Recommendation, more bool, err error)
}
