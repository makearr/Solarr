package discogs_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/discogs"
)

type answers struct{ asked *[]string }

func (a answers) GetJSON(_ context.Context, address string, _ int64, into any) error {
	*a.asked = append(*a.asked, address)
	body := `{"results":[{"id":7,"title":"Other Name - Tidal Maps"},{"id":9,"title":"Ada Quill - Tidal Maps"}]}`
	if strings.Contains(address, "/releases/") {
		body = `{"id":9,"community":{"have":50,"want":12,"rating":{"count":8,"average":4.25}}}`
	}
	return json.Unmarshal([]byte(body), into)
}

func TestFindReadsTheRatingOfTheExactArtistAndTitle(t *testing.T) {
	var asked []string
	client := discogs.New(answers{&asked}, func() string { return "tok" })
	got, err := client.Find(t.Context(), "Ada Quill", "Tidal Maps")
	if want := (discogs.Release{ID: 9, Rating: 4.25, Votes: 8, Have: 50, Want: 12}); err != nil || got != want {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	if len(asked) != 2 || !strings.Contains(asked[1], "/releases/9?token=tok") {
		t.Errorf("asked %v", asked)
	}
}

func TestFindWithoutAMatchOrAToken(t *testing.T) {
	var asked []string
	if _, err := discogs.New(answers{&asked}, func() string { return "tok" }).Find(t.Context(), "Nobody", "Tidal Maps"); !errors.Is(err, discogs.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	asked = nil
	if _, err := discogs.New(answers{&asked}, func() string { return "" }).Find(t.Context(), "A", "B"); !errors.Is(err, discogs.ErrNoKey) || len(asked) != 0 {
		t.Errorf("err = %v, asked %v", err, asked)
	}
}
