// Package lbfeeds reads ListenBrainz's sitewide charts and release calendar as recommendation feeds of music libraries.
package lbfeeds

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/listenbrainz"
)

// sourceKey is the provider's key in the metadata settings, which switch the feeds off with the provider.
const sourceKey = "listenbrainz"

// Charts is the part of the ListenBrainz client the feeds read.
type Charts interface {
	SitewideArtists(ctx context.Context, period string, offset, count int) ([]listenbrainz.ChartArtist, int, error)
	SitewideReleaseGroups(ctx context.Context, period string, offset, count int) ([]listenbrainz.ChartReleaseGroup, int, error)
	UpcomingReleases(ctx context.Context, days int) ([]listenbrainz.FreshRelease, error)
}

// The add kinds of music, as media.Descriptor names them.
const (
	KindArtist = "artist"
	KindAlbum  = "album"
)

// NoRatingsNote is what the rated lists say: ListenBrainz counts listens, it keeps no ratings.
const NoRatingsNote = "ListenBrainz keeps no ratings"

const (
	pageSize = 20
	// upcomingDays is how far ahead the release calendar is read.
	upcomingDays = 30
	// albumType is the primary type of a release group music adds as an album; singles and EPs are left out of the calendar.
	albumType = "Album"
	// calendarDepth makes the calendar's own order the popularity, as a release not out yet has no listens.
	calendarDepth = 100000
)

// Feeds recommends artists and albums from ListenBrainz, which names them by MusicBrainz id.
type Feeds struct {
	Charts Charts
	// ArtistProvider and AlbumProvider are the provider keys the type adds an artist and an album by.
	ArtistProvider, AlbumProvider string
}

var _ media.Recommending = Feeds{}

func feed(kind string, listType media.RecommendationType) media.RecommendationFeed {
	return media.RecommendationFeed{Type: listType, Kind: kind, Source: "ListenBrainz", SourceKey: sourceKey}
}

// RecommendationFeeds implements media.Recommending; ListenBrainz needs no key.
func (Feeds) RecommendationFeeds(context.Context, int64) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	return []media.RecommendationFeed{
		feed(KindArtist, media.RecommendTrending), feed(KindAlbum, media.RecommendTrending), feed(KindAlbum, media.RecommendUpcoming),
	}, map[media.RecommendationFeed]string{
		feed(KindArtist, media.RecommendHighestRated): NoRatingsNote, feed(KindAlbum, media.RecommendHighestRated): NoRatingsNote,
	}
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	offset := (query.Page - 1) * pageSize
	switch {
	case query.Feed.Kind == KindArtist && query.Feed.Type == media.RecommendTrending:
		rows, total, err := f.Charts.SitewideArtists(ctx, listenbrainz.RangeWeek, offset, pageSize)
		if err != nil {
			return nil, false, fmt.Errorf("listenbrainz artists: %w", err)
		}
		items := make([]media.Recommendation, 0, len(rows))
		for _, row := range rows {
			if row.MBID != "" {
				items = append(items, media.Recommendation{
					Kind: KindArtist, Provider: f.ArtistProvider, ExternalID: row.MBID, Title: row.Name, Popularity: float64(row.Listens),
				})
			}
		}
		return items, offset+len(rows) < total, nil
	case query.Feed.Kind == KindAlbum && query.Feed.Type == media.RecommendTrending:
		rows, total, err := f.Charts.SitewideReleaseGroups(ctx, listenbrainz.RangeWeek, offset, pageSize)
		if err != nil {
			return nil, false, fmt.Errorf("listenbrainz albums: %w", err)
		}
		return f.albums(rows), offset+len(rows) < total, nil
	case query.Feed.Kind == KindAlbum && query.Feed.Type == media.RecommendUpcoming:
		return f.upcoming(ctx, query.Page)
	}
	return nil, false, fmt.Errorf("listenbrainz: no feed %s of %s: %w", query.Feed.Type, query.Feed.Kind, media.ErrNotWired)
}

// albums keeps the first row of each release group: the chart lists a release group once for each release that was played.
func (f Feeds) albums(rows []listenbrainz.ChartReleaseGroup) []media.Recommendation {
	items := make([]media.Recommendation, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if row.MBID == "" || seen[row.MBID] {
			continue
		}
		seen[row.MBID] = true
		items = append(items, media.Recommendation{
			Kind: KindAlbum, Provider: f.AlbumProvider, ExternalID: row.MBID, Title: row.Title, Subtitle: row.Artist, PosterURL: coverOf(row.CoverRelease),
			Popularity: float64(row.Listens),
		})
	}
	return items
}

// upcoming pages through the calendar, which comes whole in one answer, soonest first.
func (f Feeds) upcoming(ctx context.Context, page int) ([]media.Recommendation, bool, error) {
	rows, err := f.Charts.UpcomingReleases(ctx, upcomingDays)
	if err != nil {
		return nil, false, fmt.Errorf("listenbrainz upcoming: %w", err)
	}
	var albums []listenbrainz.FreshRelease
	seen := map[string]bool{}
	for _, row := range rows {
		if row.PrimaryType == albumType && row.ReleaseGroupMBID != "" && !seen[row.ReleaseGroupMBID] {
			seen[row.ReleaseGroupMBID] = true
			albums = append(albums, row)
		}
	}
	start := min((page-1)*pageSize, len(albums))
	end := min(start+pageSize, len(albums))
	items := make([]media.Recommendation, 0, end-start)
	for index, row := range albums[start:end] {
		items = append(items, media.Recommendation{
			Kind: KindAlbum, Provider: f.AlbumProvider, ExternalID: row.ReleaseGroupMBID, Title: row.Title, Subtitle: row.Artist,
			PosterURL: coverOf(row.CoverRelease), ReleasedOn: row.ReleasedOn, Year: yearOf(row.ReleasedOn), Popularity: float64(calendarDepth - start - index),
		})
	}
	return items, end < len(albums), nil
}

// coverOf is the Cover Art Archive's front picture of a release, empty when the chart names none.
func coverOf(release string) string {
	if release == "" {
		return ""
	}
	return "https://coverartarchive.org/release/" + release + "/front-500"
}

func yearOf(day string) int64 {
	var year int64
	if _, err := fmt.Sscanf(day, "%4d", &year); err != nil {
		return 0
	}
	return year
}
