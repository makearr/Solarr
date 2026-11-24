package tv

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/identify/parsetv"
)

// A download folder with a season and no year ("Show.Name.S01") is a release name whose own title is the series.
func TestAFolderOfASeasonWithoutAYearReadsAsTheSeriesTitle(t *testing.T) {
	for name, want := range map[string]string{
		"Example.Show.S01":              "Example Show",
		"Example.Show.S02.1080p.WEB-DL": "Example Show",
		"Example Show S03 COMPLETE":     "Example Show",
		"Example.Show.Reborn.S01-GRP":   "Example Show Reborn",
	} {
		info, ok := parsetv.Parse(name, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		if !ok || !looksLikeRelease(name) || GuessFolder(info.SeriesTitle).Title != want {
			t.Errorf("%q: parsed %v, release %v, title %q; want %q", name, ok, looksLikeRelease(name), info.SeriesTitle, want)
		}
	}
}
