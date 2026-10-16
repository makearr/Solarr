package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestShowNFOIDsReadsTheCurrentAndTheLegacyForms(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, fsys, "/lib/Current/tvshow.nfo", `<?xml version="1.0"?><tvshow><title>Current</title>
		<uniqueid type="tvdb" default="true">1234</uniqueid><uniqueid type="tmdb">55</uniqueid><uniqueid type="imdb">tt0903747</uniqueid></tvshow>`)
	writeFile(t, fsys, "/lib/Legacy/tvshow.nfo", `<tvshow><tvdbid>77</tvdbid><imdb_id>tt42</imdb_id></tvshow>`)
	writeFile(t, fsys, "/lib/Broken/tvshow.nfo", `not xml at all <<<`)

	for folder, want := range map[string]tv.FolderGuess{
		"/lib/Current": {TVDBID: 1234, TMDBID: 55, IMDbID: "tt0903747"},
		"/lib/Legacy":  {TVDBID: 77, IMDbID: "tt42"},
		"/lib/Broken":  {},
		"/lib/Missing": {},
	} {
		if got := tv.ShowNFOIDs(fsys, folder); got != want {
			t.Errorf("%s: %+v, want %+v", folder, got, want)
		}
	}
}
