package music_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

type recordsByID map[string]specprovider.Record

func (r recordsByID) ByID(_ context.Context, namespace, id string) (specprovider.Record, error) {
	if record, ok := r[namespace+":"+id]; ok {
		return record, nil
	}
	return specprovider.Record{}, errors.New("not found")
}

func TestDefinitionArtistArtFillsOnlyWhatTheEarlierSourceLeftEmpty(t *testing.T) {
	definition := recordsByID{"musicbrainz:a1": {Overview: "From the definition.", Images: []specprovider.Image{{Kind: "photo", URL: "https://img.example/a1.jpg"}}}}
	earlier := func(context.Context, string) (string, string, error) { return "", "Earlier biography.", nil }
	art := music.DefinitionArtistArt(earlier, func() []music.RecordProvider { return []music.RecordProvider{definition} })
	photo, biography, err := art(t.Context(), "a1")
	if err != nil || photo != "https://img.example/a1.jpg" || biography != "Earlier biography." {
		t.Fatalf("got %q %q %v, want the definition's photo and the earlier biography", photo, biography, err)
	}
	photo, biography, _ = art(t.Context(), "unknown")
	if photo != "" || biography != "Earlier biography." {
		t.Fatalf("an unknown artist changed the answer: %q %q", photo, biography)
	}
}
