package itunes_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/itunes"
)

type page struct{ want, body string }

func (p page) GetJSON(_ context.Context, address string, _ int64, into any) error {
	if !strings.Contains(address, p.want) {
		return errors.New("unexpected " + address)
	}
	return json.Unmarshal([]byte(p.body), into)
}

func TestAlbumByUPC_TakesTheFirstAlbumWithArtworkAtALargeSize(t *testing.T) {
	client := itunes.New(page{"upc=0123456789012", `{"results":[{"collectionId":1,"collectionName":"No Art"},{"collectionId":2,"collectionName":"Example Album","artistName":"Example Band","artworkUrl100":"https://img.example/a/100x100bb.jpg"}]}`})
	got, err := client.AlbumByUPC(t.Context(), "0123456789012")
	if err != nil || got.CollectionID != 2 || got.Cover() != "https://img.example/a/1200x1200bb.jpg" {
		t.Errorf("album = %+v (%v), cover %q", got, err, got.Cover())
	}
}

func TestAlbumByUPC_NoResultIsNotFound(t *testing.T) {
	if _, err := itunes.New(page{"upc=", `{"results":[]}`}).AlbumByUPC(t.Context(), "1"); !errors.Is(err, itunes.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
