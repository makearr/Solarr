// Package tmdbfeeds reads TMDB's charts as the recommendation feeds of film and TV libraries.
package tmdbfeeds

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Charts is the part of the TMDB client the feeds read.
type Charts interface {
	Discover(ctx context.Context, media string, query tmdb.DiscoverQuery, page int) (tmdb.ListPage, error)
	Trending(ctx context.Context, media, window string, page int) (tmdb.ListPage, error)
}

// Detail is what a chart row lacks: the networks or studios and the certification.
type Detail struct {
	Networks      []string
	Certification string
}

// NoKeyReason is what an unusable feed says when the owner has stored no TMDB key.
const NoKeyReason = "needs the TMDB key, under Settings, Metadata"

// Votes a feed asks of what it ranks by rating, so a handful of votes cannot top a chart.
const (
	ratedVotesFilm, classicVotesFilm = 500, 1000
	ratedVotesTV, classicVotesTV     = 200, 300
)

// Feeds recommends films (Media tmdb.MediaMovie) or shows (tmdb.MediaTV) from TMDB.
type Feeds struct {
	Media  string
	Charts Charts
	Clock  clock.Clock
	// Configured says the owner stored a TMDB key.
	Configured func() bool
	// Detail reads one entry's networks and certification; nil leaves them empty.
	Detail func(ctx context.Context, id int64) (Detail, error)
}

var _ media.Recommending = Feeds{}

var feedTypes = []media.RecommendationType{media.RecommendTrending, media.RecommendHighestRated, media.RecommendUpcoming, media.RecommendClassics}

// RecommendationFeeds implements media.Recommending.
func (f Feeds) RecommendationFeeds(context.Context) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feeds := make([]media.RecommendationFeed, len(feedTypes))
	for index, one := range feedTypes {
		feeds[index] = media.RecommendationFeed{Type: one, Source: "TMDB", HasRating: true}
	}
	if f.Configured != nil && !f.Configured() {
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
	page, err := f.read(ctx, query)
	if err != nil {
		return nil, false, fmt.Errorf("tmdb %s %s: %w", f.Media, query.Feed.Type, err)
	}
	items := make([]media.Recommendation, 0, len(page.Items))
	for _, entry := range page.Items {
		item := f.recommendation(entry)
		if f.Detail != nil {
			if detail, err := f.Detail(ctx, entry.ID); err == nil {
				item.Networks, item.Traits.Rating = detail.Networks, detail.Certification
			}
		}
		items = append(items, item)
	}
	return items, query.Page < page.TotalPages, nil
}

func (f Feeds) read(ctx context.Context, query media.RecommendQuery) (tmdb.ListPage, error) {
	today := f.Clock.Now()
	day := "2006-01-02"
	votesRated, votesClassic := ratedVotesFilm, classicVotesFilm
	if f.Media == tmdb.MediaTV {
		votesRated, votesClassic = ratedVotesTV, classicVotesTV
	}
	discover := tmdb.DiscoverQuery{WithoutGenres: genreIDs(query.ExcludeGenres)}
	if len(query.Languages) == 1 {
		discover.Language = query.Languages[0]
	}
	switch query.Feed.Type {
	case media.RecommendTrending:
		return f.Charts.Trending(ctx, f.Media, tmdb.TrendingWeek, query.Page)
	case media.RecommendHighestRated:
		discover.SortBy, discover.MinVotes = tmdb.SortRating, votesRated
	case media.RecommendUpcoming:
		discover.SortBy, discover.ReleasedFrom = tmdb.SortPopularity, today.Format(day)
	case media.RecommendClassics:
		discover.SortBy, discover.MinVotes = tmdb.SortRating, votesClassic
		discover.ReleasedTo = today.AddDate(-query.ClassicAgeYears, 0, 0).Format(day)
	default:
		return tmdb.ListPage{}, fmt.Errorf("no such feed %q", query.Feed.Type)
	}
	return f.Charts.Discover(ctx, f.Media, discover, query.Page)
}

func (f Feeds) recommendation(entry tmdb.ListItem) media.Recommendation {
	title, released, provider := entry.Title, entry.ReleaseDate, tmdb.KeyMovie
	if f.Media == tmdb.MediaTV {
		title, released, provider = entry.Name, entry.FirstAirDate, tmdb.KeySeries
	}
	year, _ := strconv.ParseInt(strings.SplitN(released, "-", 2)[0], 10, 64)
	return media.Recommendation{
		Provider: provider, ExternalID: strconv.FormatInt(entry.ID, 10), Title: title, Overview: entry.Overview, PosterURL: tmdb.ImageURL(entry.PosterPath),
		Year: year, ReleasedOn: released, Popularity: entry.Popularity, Rating: entry.VoteAverage, Votes: entry.VoteCount,
		Traits: media.Traits{Genres: tmdb.Genres(entry.GenreIDs), OriginalLanguage: entry.OriginalLanguage, OriginCountries: entry.OriginCountry},
	}
}

// genreIDs names TMDB's ids for genre names, however they are cased; a name TMDB has no id for is left to the admit rules.
func genreIDs(names []string) []int64 {
	var ids []int64
	for _, name := range names {
		for id, known := range tmdb.GenreNames {
			if strings.EqualFold(known, strings.TrimSpace(name)) {
				ids = append(ids, id)
			}
		}
	}
	slices.Sort(ids)
	return ids
}
