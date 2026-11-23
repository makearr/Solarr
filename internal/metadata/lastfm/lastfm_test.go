package lastfm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/lastfm"
)

type answer struct {
	body  string
	asked *string
}

func (a answer) GetJSON(_ context.Context, address string, _ int64, into any) error {
	*a.asked = address
	return json.Unmarshal([]byte(a.body), into)
}

func key(value string) func() string { return func() string { return value } }

func TestAlbumInfoReadsListenersAndPlays(t *testing.T) {
	var asked string
	client := lastfm.New(answer{asked: &asked, body: `{"album":{"name":"Tidal Maps","artist":"Ada Quill","mbid":"m-1","listeners":"1200","playcount":"98000"}}`}, key("k1"))
	got, err := client.AlbumInfo(t.Context(), "Ada Quill", "Tidal Maps")
	if want := (lastfm.Album{Name: "Tidal Maps", Artist: "Ada Quill", MBID: "m-1", Listeners: 1200, Plays: 98000}); err != nil || got != want {
		t.Fatalf("AlbumInfo = %+v, %v", got, err)
	}
	parsed, _ := url.Parse(asked)
	if q := parsed.Query(); q.Get("api_key") != "k1" || q.Get("method") != "album.getinfo" || q.Get("artist") != "Ada Quill" || q.Get("album") != "Tidal Maps" {
		t.Errorf("asked %s", asked)
	}
}

func TestAlbumInfoUnknownAlbumIsNotFound(t *testing.T) {
	var asked string
	client := lastfm.New(answer{asked: &asked, body: `{"error":6,"message":"Album not found"}`}, key("k1"))
	if _, err := client.AlbumInfo(t.Context(), "A", "B"); !errors.Is(err, lastfm.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestNoKeyAsksNothing(t *testing.T) {
	var asked string
	client := lastfm.New(answer{asked: &asked, body: `{}`}, key(""))
	if _, err := client.AlbumInfo(t.Context(), "A", "B"); !errors.Is(err, lastfm.ErrNoKey) || asked != "" || client.Configured() {
		t.Errorf("err = %v, asked %q", err, asked)
	}
}

func TestSimilarReadsMatches(t *testing.T) {
	var asked string
	client := lastfm.New(answer{asked: &asked, body: `{"similarartists":{"artist":[{"name":"Bo Rend","mbid":"","match":"0.91"},{"name":"Cy Fenn","mbid":"m-2","match":"0.5"}]}}`}, key("k"))
	got, err := client.Similar(t.Context(), "Ada Quill", 2)
	want := []lastfm.SimilarArtist{{Name: "Bo Rend", Match: 0.91}, {Name: "Cy Fenn", MBID: "m-2", Match: 0.5}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Similar = %+v, %v", got, err)
	}
}

func TestTopArtistsReadsAPageAndWhetherAnotherFollows(t *testing.T) {
	var asked string
	client := lastfm.New(answer{asked: &asked, body: `{"artists":{"artist":[{"name":"Ada Quill","mbid":"m-1","listeners":"900","playcount":"5000"},{"name":"Bo Rend","mbid":"","listeners":"x","playcount":"7"}],"@attr":{"page":"1","totalPages":"3"}}}`}, key("k"))
	got, more, err := client.TopArtists(t.Context(), 1, 2)
	want := []lastfm.ChartArtist{{Name: "Ada Quill", MBID: "m-1", Listeners: 900, Plays: 5000}, {Name: "Bo Rend", Plays: 7}}
	if err != nil || !more || !reflect.DeepEqual(got, want) {
		t.Fatalf("TopArtists = %+v, %v, %v", got, more, err)
	}
	parsed, _ := url.Parse(asked)
	if q := parsed.Query(); q.Get("method") != "chart.gettopartists" || q.Get("page") != "1" || q.Get("limit") != "2" {
		t.Errorf("asked %s", asked)
	}
}

func TestTagTopAlbumsNamesTheArtistAndTheLastPage(t *testing.T) {
	var asked string
	client := lastfm.New(answer{asked: &asked, body: `{"albums":{"album":[{"name":"Tidal Maps","mbid":"m-3","artist":{"name":"Ada Quill"}}],"@attr":{"tag":"folk","page":"2","totalPages":"2"}}}`}, key("k"))
	got, more, err := client.TagTopAlbums(t.Context(), "folk", 2, 1)
	if want := []lastfm.ChartAlbum{{Name: "Tidal Maps", Artist: "Ada Quill", MBID: "m-3"}}; err != nil || more || !reflect.DeepEqual(got, want) {
		t.Fatalf("TagTopAlbums = %+v, %v, %v", got, more, err)
	}
	parsed, _ := url.Parse(asked)
	if q := parsed.Query(); q.Get("method") != "tag.gettopalbums" || q.Get("tag") != "folk" {
		t.Errorf("asked %s", asked)
	}
}

func TestArtistTopAlbumsAndAnUnknownArtist(t *testing.T) {
	var asked string
	client := lastfm.New(answer{asked: &asked, body: `{"topalbums":{"album":[{"name":"Tidal Maps","mbid":"","playcount":4200,"artist":{"name":"Ada Quill"}}]}}`}, key("k"))
	got, err := client.ArtistTopAlbums(t.Context(), "Ada Quill", 1)
	if want := []lastfm.ChartAlbum{{Name: "Tidal Maps", Artist: "Ada Quill", Plays: 4200}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ArtistTopAlbums = %+v, %v", got, err)
	}
	missing := lastfm.New(answer{asked: &asked, body: `{"error":6,"message":"The artist you supplied could not be found"}`}, key("k"))
	if _, err := missing.ArtistTopAlbums(t.Context(), "Nobody", 1); !errors.Is(err, lastfm.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
