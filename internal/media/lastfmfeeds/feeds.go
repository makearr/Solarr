// Package lastfmfeeds reads Last.fm's charts as the recommendation feeds of music libraries.
package lastfmfeeds

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/lastfm"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// sourceKey is the provider's key in the metadata settings, which switch the feeds off with the provider.
const sourceKey = "lastfm"

// Charts is the part of the Last.fm client the feeds read.
type Charts interface {
	TopArtists(ctx context.Context, page, limit int) ([]lastfm.ChartArtist, bool, error)
	TagTopAlbums(ctx context.Context, tag string, page, limit int) ([]lastfm.ChartAlbum, bool, error)
	Similar(ctx context.Context, artist string, limit int) ([]lastfm.SimilarArtist, error)
	ArtistTopAlbums(ctx context.Context, artist string, limit int) ([]lastfm.ChartAlbum, error)
}

// Names finds the ids music adds by for what Last.fm names, which it often gives without one; an entry nothing resolves is left out.
type Names interface {
	Artist(ctx context.Context, name string) (id string, found bool, err error)
	// Album answers the release group's id and the year of its first release, 0 when unknown.
	Album(ctx context.Context, artist, title string) (id string, year int, found bool, err error)
}

// Taste is what a library holds, which seeds the feeds that follow it.
type Taste interface {
	// Artists are the library's most-held artists, most first.
	Artists(ctx context.Context, categoryID int64, limit int) ([]string, error)
	// Genres are the library's most-held genres, most first.
	Genres(ctx context.Context, categoryID int64, limit int) ([]string, error)
}

// The add kinds of music, as media.Descriptor names them.
const (
	KindArtist = "artist"
	KindAlbum  = "album"
)

// Reasons an unusable feed gives.
const (
	NoKeyReason    = "needs the Last.fm key, under Settings, Metadata"
	NoCalendarNote = "Last.fm has no release calendar"
)

const (
	pageSize = 20
	// seeds is how many held artists or genres a feed follows; each is one page.
	seeds = 5
	// similarPerSeed is how many alike artists a seed's page of albums reads.
	similarPerSeed = 5
	// tagPages is how many pages deep one genre is read before the feed moves on.
	tagPages = 3
)

// Feeds recommends artists and albums from Last.fm; the library's own artists and genres decide which ones.
type Feeds struct {
	Charts Charts
	Names  Names
	Taste  Taste
	// ArtistProvider and AlbumProvider are the provider keys the type adds an artist and an album by.
	ArtistProvider, AlbumProvider string
	Clock                         clock.Clock
	// Configured says the owner stored a Last.fm key.
	Configured func() bool
}

var _ media.Recommending = Feeds{}

func feed(kind string, listType media.RecommendationType) media.RecommendationFeed {
	return media.RecommendationFeed{Type: listType, Kind: kind, Source: "Last.fm", SourceKey: sourceKey}
}

// RecommendationFeeds implements media.Recommending; Last.fm carries no ratings, so auto-add skips its rating gate.
func (f Feeds) RecommendationFeeds(context.Context, int64) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feeds := []media.RecommendationFeed{
		feed(KindArtist, media.RecommendTrending), feed(KindAlbum, media.RecommendTrending),
		feed(KindArtist, media.RecommendHighestRated), feed(KindAlbum, media.RecommendHighestRated),
		feed(KindAlbum, media.RecommendClassics),
	}
	unavailable := map[media.RecommendationFeed]string{
		feed(KindArtist, media.RecommendUpcoming): NoCalendarNote, feed(KindAlbum, media.RecommendUpcoming): NoCalendarNote,
	}
	if f.Configured != nil && !f.Configured() {
		for _, one := range feeds {
			unavailable[one] = NoKeyReason
		}
		return nil, unavailable
	}
	return feeds, unavailable
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	var (
		items []media.Recommendation
		more  bool
		err   error
	)
	switch {
	case query.Feed.Kind == KindArtist && query.Feed.Type == media.RecommendTrending:
		items, more, err = f.trendingArtists(ctx, query)
	case query.Feed.Kind == KindArtist && query.Feed.Type == media.RecommendHighestRated:
		items, more, err = f.similarArtists(ctx, query)
	case query.Feed.Kind == KindAlbum && query.Feed.Type == media.RecommendHighestRated:
		items, more, err = f.similarAlbums(ctx, query)
	case query.Feed.Kind == KindAlbum && (query.Feed.Type == media.RecommendTrending || query.Feed.Type == media.RecommendClassics):
		items, more, err = f.genreAlbums(ctx, query)
	default:
		return nil, false, fmt.Errorf("lastfm: no feed %s of %s: %w", query.Feed.Type, query.Feed.Kind, media.ErrNotWired)
	}
	if err != nil {
		if errors.Is(err, lastfm.ErrNoKey) {
			return nil, false, fmt.Errorf("lastfm %s: %w", query.Feed.Type, media.ErrSourceUnavailable)
		}
		return nil, false, fmt.Errorf("lastfm %s %s: %w", query.Feed.Kind, query.Feed.Type, err)
	}
	return items, more, nil
}

func (f Feeds) artist(ctx context.Context, name, mbid string) (string, bool, error) {
	if mbid != "" {
		return mbid, true, nil
	}
	return f.Names.Artist(ctx, name)
}

func (f Feeds) trendingArtists(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	entries, more, err := f.Charts.TopArtists(ctx, query.Page, pageSize)
	if err != nil {
		return nil, false, err
	}
	var items []media.Recommendation
	for _, entry := range entries {
		id, found, err := f.artist(ctx, entry.Name, entry.MBID)
		if err != nil {
			return nil, false, err
		}
		if found {
			items = append(items, media.Recommendation{
				Kind: KindArtist, Provider: f.ArtistProvider, ExternalID: id, Title: entry.Name, Popularity: float64(entry.Listeners), Votes: entry.Listeners,
			})
		}
	}
	return items, more, nil
}

// seedAt is the seed one page follows and whether another page follows it.
func seedAt(all []string, page int) (seed string, ok, more bool) {
	if page < 1 || page > len(all) {
		return "", false, false
	}
	return all[page-1], true, page < len(all)
}

func (f Feeds) similarArtists(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	held, err := f.Taste.Artists(ctx, query.CategoryID, seeds)
	if err != nil {
		return nil, false, err
	}
	seed, ok, more := seedAt(held, query.Page)
	if !ok {
		return nil, false, nil
	}
	similar, err := f.Charts.Similar(ctx, seed, pageSize)
	if err != nil && !errors.Is(err, lastfm.ErrNotFound) {
		return nil, false, err
	}
	var items []media.Recommendation
	for _, entry := range similar {
		id, found, err := f.artist(ctx, entry.Name, entry.MBID)
		if err != nil {
			return nil, false, err
		}
		if found {
			items = append(items, media.Recommendation{
				Kind: KindArtist, Provider: f.ArtistProvider, ExternalID: id, Title: entry.Name, Subtitle: "Similar to " + seed, Popularity: entry.Match * 1000,
			})
		}
	}
	return items, more, nil
}

func (f Feeds) album(ctx context.Context, artist, title string) (media.Recommendation, bool, error) {
	id, year, ok, err := f.Names.Album(ctx, artist, title)
	if err != nil || !ok {
		return media.Recommendation{}, false, err
	}
	return media.Recommendation{Kind: KindAlbum, Provider: f.AlbumProvider, ExternalID: id, Title: title, Subtitle: artist, Year: int64(year)}, true, nil
}

func (f Feeds) similarAlbums(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	held, err := f.Taste.Artists(ctx, query.CategoryID, seeds)
	if err != nil {
		return nil, false, err
	}
	seed, ok, more := seedAt(held, query.Page)
	if !ok {
		return nil, false, nil
	}
	similar, err := f.Charts.Similar(ctx, seed, similarPerSeed)
	if err != nil && !errors.Is(err, lastfm.ErrNotFound) {
		return nil, false, err
	}
	var items []media.Recommendation
	for _, entry := range similar {
		albums, err := f.Charts.ArtistTopAlbums(ctx, entry.Name, 1)
		if errors.Is(err, lastfm.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		for _, one := range albums {
			item, found, err := f.album(ctx, entry.Name, one.Name)
			if err != nil {
				return nil, false, err
			}
			if found {
				item.Popularity = entry.Match * 1000
				items = append(items, item)
			}
		}
	}
	return items, more, nil
}

// genreAlbums reads the top albums of the library's genres in turn: page 1 is the first genre's first page, page 2 the second genre's.
func (f Feeds) genreAlbums(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	genres, err := f.Taste.Genres(ctx, query.CategoryID, seeds)
	if err != nil {
		return nil, false, err
	}
	if len(genres) == 0 || query.Page < 1 || query.Page > len(genres)*tagPages {
		return nil, false, nil
	}
	genre, tagPage := genres[(query.Page-1)%len(genres)], (query.Page-1)/len(genres)+1
	entries, _, err := f.Charts.TagTopAlbums(ctx, genre, tagPage, pageSize)
	if err != nil {
		return nil, false, err
	}
	newest := f.Clock.Now().Year() - query.ClassicAgeYears
	var items []media.Recommendation
	for index, entry := range entries {
		item, found, err := f.album(ctx, entry.Artist, entry.Name)
		if err != nil {
			return nil, false, err
		}
		if !found || (query.Feed.Type == media.RecommendClassics && (item.Year == 0 || int(item.Year) > newest)) {
			continue
		}
		// The chart's own order is the popularity, as Last.fm gives no count with a tag's albums.
		item.Popularity = float64(len(entries) - index)
		item.Traits.Genres = []string{genre}
		items = append(items, item)
	}
	return items, query.Page < len(genres)*tagPages, nil
}
