package tv

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// As text, episode 100 sorts before episode 99, which would name a file holding both backwards.
func TestEpisodeNaming_ListsEpisodesInNumberOrder(t *testing.T) {
	file := fileEpisodes{parts: []library.ResolvedPart{
		{MediaRef: MediaRef(1, 100), Title: "Hundred"}, {MediaRef: MediaRef(1, 99), Title: "Ninety-nine"},
	}}
	got := episodeNaming(library.SeriesDetailRow{}, file).Episodes
	want := []EpisodeNumbering{{Number: 99, Title: "Ninety-nine"}, {Number: 100, Title: "Hundred"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("episodes = %+v, want %+v", got, want)
	}
}

func TestParseMediaRef(t *testing.T) {
	season, episode, ok := ParseMediaRef(MediaRef(3, 107))
	if season != 3 || episode != 107 || !ok {
		t.Errorf("ParseMediaRef = %d, %d, %v; want 3, 107, true", season, episode, ok)
	}
	if _, _, ok := ParseMediaRef("1:001"); ok {
		t.Error("a music ref parsed as an episode")
	}
}
