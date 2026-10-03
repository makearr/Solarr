package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/tv"
)

func TestGuessZoneKnowsTwoAndThreeLetterCountryCodes(t *testing.T) {
	for country, want := range map[string]string{"US": "America/New_York", "usa": "America/New_York", "jpn": "Asia/Tokyo", "GB": "Europe/London", "zz": ""} {
		if got := tv.GuessZone(country); got != want {
			t.Errorf("GuessZone(%q) = %q, want %q", country, got, want)
		}
	}
}
