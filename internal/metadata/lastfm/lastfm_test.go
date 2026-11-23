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
