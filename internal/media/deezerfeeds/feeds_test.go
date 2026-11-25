package deezerfeeds_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/deezerfeeds"
	"github.com/makearr/Solarr/internal/metadata/deezer"
)

type charts struct{ albums []deezer.ChartEntry }

func (charts) ChartArtists(context.Context, int, int) ([]deezer.ChartEntry, int, error) {
	return nil, 0, nil
}
func (c charts) ChartAlbums(context.Context, int, int) ([]deezer.ChartEntry, int, error) {
	return c.albums, 25, nil
}

type names map[string]string

func (n names) Artist(_ context.Context, name string) (string, bool, error) {
	id, ok := n[name]
	return id, ok, nil
}

func (n names) Album(_ context.Context, artist, title string) (string, int, bool, error) {
	id, ok := n[artist+"/"+title]
	return id, 2020, ok, nil
}

func TestTrendingAlbumsResolveByNameAndRankByPosition(t *testing.T) {
	f := deezerfeeds.Feeds{
		Charts: charts{albums: []deezer.ChartEntry{
			{Title: "Known Album", Artist: "Example Artist", Picture: "https://e-cdns-images.dzcdn.net/a.jpg", Position: 1},
			{Title: "Unknown Album", Artist: "Example Artist", Position: 2},
		}},
		Names: names{"Example Artist/Known Album": "rg-1"}, ArtistProvider: "musicbrainz:artist", AlbumProvider: "musicbrainz:release-group",
	}
	query := media.RecommendQuery{Feed: media.RecommendationFeed{Type: media.RecommendTrending, Kind: deezerfeeds.KindAlbum, SourceKey: "deezer"}, Page: 1}
	items, more, err := f.Recommend(t.Context(), query)
	want := []media.Recommendation{{
		Kind: "album", Provider: "musicbrainz:release-group", ExternalID: "rg-1", Title: "Known Album", Subtitle: "Example Artist", Year: 2020,
		PosterURL: "https://e-cdns-images.dzcdn.net/a.jpg", Popularity: 999,
	}}
	if err != nil || !more || !reflect.DeepEqual(items, want) {
		t.Errorf("items %+v more %v err %v", items, more, err)
	}
}
