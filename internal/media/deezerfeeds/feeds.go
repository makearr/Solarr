// Package deezerfeeds reads Deezer's charts as recommendation feeds of music libraries.
package deezerfeeds

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/deezer"
)

// sourceKey is the provider's key in the metadata settings, which switch the feeds off with the provider.
const sourceKey = "deezer"

// Charts is the part of the Deezer client the feeds read.
type Charts interface {
	ChartArtists(ctx context.Context, index, limit int) ([]deezer.ChartEntry, int, error)
	ChartAlbums(ctx context.Context, index, limit int) ([]deezer.ChartEntry, int, error)
}

// Names finds the ids music adds by for what Deezer names, which it names by its own ids only; an entry nothing resolves is left out.
type Names interface {
	Artist(ctx context.Context, name string) (id string, found bool, err error)
	// Album answers the release group's id and the year of its first release, 0 when unknown.
	Album(ctx context.Context, artist, title string) (id string, year int, found bool, err error)
}

// The add kinds of music, as media.Descriptor names them.
const (
	KindArtist = "artist"
	KindAlbum  = "album"
)

const (
	pageSize = 20
	// chartDepth makes the chart's position the popularity, as a chart entry carries no count.
	chartDepth = 1000
)

// Feeds recommends artists and albums from Deezer's chart of the country the instance runs in.
type Feeds struct {
	Charts Charts
	Names  Names
	// ArtistProvider and AlbumProvider are the provider keys the type adds an artist and an album by.
	ArtistProvider, AlbumProvider string
}

var _ media.Recommending = Feeds{}

func feed(kind string) media.RecommendationFeed {
	return media.RecommendationFeed{Type: media.RecommendTrending, Kind: kind, Source: "Deezer", SourceKey: sourceKey}
}

// RecommendationFeeds implements media.Recommending; Deezer's charts need no key and carry no ratings.
func (Feeds) RecommendationFeeds(context.Context, int64) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	return []media.RecommendationFeed{feed(KindArtist), feed(KindAlbum)}, nil
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	if query.Feed.Type != media.RecommendTrending {
		return nil, false, fmt.Errorf("deezer: no feed %s: %w", query.Feed.Type, media.ErrNotWired)
	}
	index := (query.Page - 1) * pageSize
	read := f.Charts.ChartAlbums
	if query.Feed.Kind == KindArtist {
		read = f.Charts.ChartArtists
	}
	entries, total, err := read(ctx, index, pageSize)
	if err != nil {
		return nil, false, fmt.Errorf("deezer %s chart: %w", query.Feed.Kind, err)
	}
	items := make([]media.Recommendation, 0, len(entries))
	for _, entry := range entries {
		item, found, err := f.resolve(ctx, query.Feed.Kind, entry)
		if err != nil {
			return nil, false, err
		}
		if found {
			item.PosterURL, item.Popularity = entry.Picture, float64(chartDepth-entry.Position)
			items = append(items, item)
		}
	}
	return items, index+len(entries) < total, nil
}

func (f Feeds) resolve(ctx context.Context, kind string, entry deezer.ChartEntry) (media.Recommendation, bool, error) {
	if kind == KindArtist {
		id, found, err := f.Names.Artist(ctx, entry.Title)
		return media.Recommendation{Kind: KindArtist, Provider: f.ArtistProvider, ExternalID: id, Title: entry.Title}, found, err
	}
	id, year, found, err := f.Names.Album(ctx, entry.Artist, entry.Title)
	return media.Recommendation{Kind: KindAlbum, Provider: f.AlbumProvider, ExternalID: id, Title: entry.Title, Subtitle: entry.Artist, Year: int64(year)}, found, err
}
