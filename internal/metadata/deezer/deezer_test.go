package deezer_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/deezer"
)

type pages map[string]string

func (p pages) GetJSON(_ context.Context, address string, _ int64, into any) error {
	for suffix, body := range p {
		if strings.Contains(address, suffix) {
			return json.Unmarshal([]byte(body), into)
		}
	}
	return errors.New("unexpected " + address)
}

func TestFind_TakesTheExactArtistAndTitleAndReadsItsFans(t *testing.T) {
	client := deezer.New(pages{
		"/search/album": `{"data":[{"id":1,"title":"Example Album (Live)","artist":{"name":"Example Band"}},{"id":2,"title":"Example Album","artist":{"name":"example band"}}]}`,
		"/album/2":      `{"id":2,"title":"Example Album","fans":1500,"explicit_lyrics":true,"cover_xl":"https://img.example/2.jpg","artist":{"name":"Example Band"}}`,
	})
	got, err := client.Find(t.Context(), "Example Band", "Example Album")
	want := deezer.Album{ID: 2, Title: "Example Album", Artist: "example band", Fans: 1500, Explicit: true, Cover: "https://img.example/2.jpg"}
	if err != nil || got != want {
		t.Errorf("Find = %+v, %v; want %+v", got, err, want)
	}
}

func TestFind_NoExactMatchIsNotFound(t *testing.T) {
	client := deezer.New(pages{"/search/album": `{"data":[{"id":1,"title":"Example Album (Live)","artist":{"name":"Example Band"}}]}`})
	if _, err := client.Find(t.Context(), "Example Band", "Example Album"); !errors.Is(err, deezer.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestByUPC_ReadsTheAlbumTheBarcodeNames(t *testing.T) {
	client := deezer.New(pages{"/album/upc:5000000000001": `{"id":7,"title":"Example Album","fans":20,"cover_xl":"https://img.example/7.jpg","artist":{"name":"Example Band"}}`})
	got, err := client.ByUPC(t.Context(), "5000000000001")
	want := deezer.Album{ID: 7, Title: "Example Album", Artist: "Example Band", Fans: 20, Cover: "https://img.example/7.jpg"}
	if err != nil || got != want {
		t.Errorf("ByUPC = %+v, %v; want %+v", got, err, want)
	}
}

func TestByUPC_AnErrorBodyIsNotFound(t *testing.T) {
	client := deezer.New(pages{"/album/upc:": `{"error":{"type":"DataException","code":800}}`})
	if _, err := client.ByUPC(t.Context(), "5000000000002"); !errors.Is(err, deezer.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestChartAlbums_AsksThePageAndReadsArtistCoverAndPosition(t *testing.T) {
	client := deezer.New(pages{"/chart/0/albums?index=20&limit=20": `{"data":[{"id":7,"title":"Example Album","cover_xl":"https://e-cdns-images.dzcdn.net/x.jpg",` +
		`"position":21,"artist":{"name":"Example Artist"}}],"total":100}`})
	got, total, err := client.ChartAlbums(t.Context(), 20, 20)
	want := []deezer.ChartEntry{{Title: "Example Album", Artist: "Example Artist", Picture: "https://e-cdns-images.dzcdn.net/x.jpg", Position: 21}}
	if err != nil || total != 100 || !reflect.DeepEqual(got, want) {
		t.Errorf("ChartAlbums = %+v, %d, %v", got, total, err)
	}
}
