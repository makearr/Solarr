package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/fanart"
)

func TestFanartArtistOptionsLabelsEachKind(t *testing.T) {
	artist := fanart.Artist{
		Thumbs:  []fanart.Image{{URL: "https://img.example/t.jpg"}},
		Logos:   []fanart.Image{{URL: "https://img.example/l.png"}},
		Banners: []fanart.Image{{URL: "https://img.example/b.jpg"}},
	}
	want := []media.ImageOption{
		{URL: "https://img.example/t.jpg", Source: "fanart.tv", Label: "Photo"},
		{URL: "https://img.example/l.png", Source: "fanart.tv", Label: "Logo"},
		{URL: "https://img.example/b.jpg", Source: "fanart.tv", Label: "Banner"},
	}
	if got := fanartArtistOptions(artist, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got := fanartArtistOptions(artist, errors.New("down")); got != nil {
		t.Fatalf("an error must offer nothing, got %+v", got)
	}
}

func TestFanartAlbumOptionsCoversThenDiscs(t *testing.T) {
	var album fanart.Album
	album.Albums = map[string]struct {
		Covers []fanart.Image `json:"albumcover"`
		Discs  []fanart.Image `json:"cdart"`
	}{"group": {Covers: []fanart.Image{{URL: "https://img.example/c.jpg"}}, Discs: []fanart.Image{{URL: "https://img.example/d.png"}}}}
	want := []media.ImageOption{
		{URL: "https://img.example/c.jpg", Source: "fanart.tv", Label: "Cover"},
		{URL: "https://img.example/d.png", Source: "fanart.tv", Label: "Disc"},
	}
	if got := fanartAlbumOptions(album, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}
