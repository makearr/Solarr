package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/tv"
)

func TestReleaseLabelNamesTheEpisodesAReleaseHolds(t *testing.T) {
	for title, want := range map[string]string{
		"Example.Show.S03E10.1080p.WEB-DL.H.264-GRP":       "S03E10",
		"Example.Show.S03E01E02E03.1080p.WEB-DL.H.264-GRP": "S03E01-E03",
		"Example.Show.S03.1080p.WEB-DL.H.264-GRP":          "S03",
		"nothing a parser can read":                        "S02",
	} {
		if got := tv.ReleaseLabel(title, "s02", addNow); got != want {
			t.Errorf("%q: got %q, want %q", title, got, want)
		}
	}
}
