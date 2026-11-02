package tv

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestAnEpisodeIsTitledByItsOwnOrAnAlternativeLanguageTitle(t *testing.T) {
	episode := library.SeriesEpisode{Title: "The Pilot", AltTitles: []string{"Der Pilot Flug"}}
	for key, want := range map[string]bool{episodeKey("the pilot"): true, episodeKey("Der Pilot Flug"): true, episodeKey("Anderer Titel"): false} {
		if got := episodeTitled(episode, key); got != want {
			t.Errorf("episodeTitled(%q) = %v, want %v", key, got, want)
		}
	}
}
