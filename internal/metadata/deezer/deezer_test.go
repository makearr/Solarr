package deezer_test

import (
	"context"
	"encoding/json"
	"errors"
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
