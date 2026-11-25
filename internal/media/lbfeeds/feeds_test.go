package lbfeeds_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/lbfeeds"
	"github.com/makearr/Solarr/internal/metadata/listenbrainz"
)

type charts struct {
	groups   []listenbrainz.ChartReleaseGroup
	releases []listenbrainz.FreshRelease
	asked    []int
}

func (c *charts) SitewideArtists(context.Context, string, int, int) ([]listenbrainz.ChartArtist, int, error) {
	return nil, 0, nil
}

func (c *charts) SitewideReleaseGroups(_ context.Context, _ string, offset, _ int) ([]listenbrainz.ChartReleaseGroup, int, error) {
	c.asked = append(c.asked, offset)
	return c.groups, 41, nil
}

func (c *charts) UpcomingReleases(context.Context, int) ([]listenbrainz.FreshRelease, error) {
	return c.releases, nil
}

func feeds(c *charts) lbfeeds.Feeds {
	return lbfeeds.Feeds{Charts: c, ArtistProvider: "musicbrainz:artist", AlbumProvider: "musicbrainz:release-group"}
}

func query(kind string, listType media.RecommendationType, page int) media.RecommendQuery {
	return media.RecommendQuery{Feed: media.RecommendationFeed{Type: listType, Kind: kind, SourceKey: "listenbrainz"}, Page: page}
}

func TestTrendingAlbumsKeepEachReleaseGroupOnceAndPageByOffset(t *testing.T) {
	c := &charts{groups: []listenbrainz.ChartReleaseGroup{
		{MBID: "rg-1", Title: "First Album", Artist: "Example Artist", CoverRelease: "rel-1", Listens: 90},
		{MBID: "rg-1", Title: "First Album", Artist: "Example Artist", CoverRelease: "rel-2", Listens: 80},
		{MBID: "rg-2", Title: "Second Album", Artist: "Other Artist", Listens: 40},
	}}
	items, more, err := feeds(c).Recommend(t.Context(), query(lbfeeds.KindAlbum, media.RecommendTrending, 2))
	if err != nil {
		t.Fatal(err)
	}
	want := []media.Recommendation{
		{Kind: "album", Provider: "musicbrainz:release-group", ExternalID: "rg-1", Title: "First Album", Subtitle: "Example Artist",
			PosterURL: "https://coverartarchive.org/release/rel-1/front-500", Popularity: 90},
		{Kind: "album", Provider: "musicbrainz:release-group", ExternalID: "rg-2", Title: "Second Album", Subtitle: "Other Artist", Popularity: 40},
	}
	if !reflect.DeepEqual(items, want) || !more || !reflect.DeepEqual(c.asked, []int{20}) {
		t.Errorf("items %+v more %v asked %v", items, more, c.asked)
	}
}

func TestUpcomingTakesAlbumsOnlySoonestFirst(t *testing.T) {
	c := &charts{releases: []listenbrainz.FreshRelease{
		{ReleaseGroupMBID: "rg-3", Title: "A Single", Artist: "Example Artist", ReleasedOn: "2030-01-01", PrimaryType: "Single"},
		{ReleaseGroupMBID: "rg-4", Title: "Next Album", Artist: "Example Artist", ReleasedOn: "2030-01-02", PrimaryType: "Album"},
	}}
	items, more, err := feeds(c).Recommend(t.Context(), query(lbfeeds.KindAlbum, media.RecommendUpcoming, 1))
	want := []media.Recommendation{{
		Kind: "album", Provider: "musicbrainz:release-group", ExternalID: "rg-4", Title: "Next Album", Subtitle: "Example Artist",
		ReleasedOn: "2030-01-02", Year: 2030, Popularity: 100000,
	}}
	if err != nil || more || !reflect.DeepEqual(items, want) {
		t.Errorf("items %+v more %v err %v", items, more, err)
	}
}

func TestNoListClaimsARating(t *testing.T) {
	usable, unavailable := feeds(&charts{}).RecommendationFeeds(t.Context(), 1)
	for _, feed := range usable {
		if feed.HasRating || feed.SourceKey != "listenbrainz" {
			t.Errorf("feed %+v", feed)
		}
	}
	if len(usable) != 3 || len(unavailable) != 2 {
		t.Errorf("usable %v unavailable %v", usable, unavailable)
	}
}
