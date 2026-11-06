package movie_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/movie"
)

func TestImporter_Fit(t *testing.T) {
	importer := movie.NewImporter(movie.ImporterConfig{})
	for name, tc := range map[string]struct {
		files []inspect.File
		want  float64
	}{
		"a film with its year":     {[]inspect.File{{RelPath: "Film (2001)/Film.2001.1080p.mkv", SizeBytes: 900}}, batch.FitLikely},
		"the film beside a sample": {[]inspect.File{{RelPath: "F/sample.mkv", SizeBytes: 9}, {RelPath: "F/Film.2001.mkv", SizeBytes: 900}}, batch.FitLikely},
		"a video naming no year":   {[]inspect.File{{RelPath: "Clip/clip.mkv", SizeBytes: 900}}, batch.FitPossible},
		"sound with a small video": {[]inspect.File{{RelPath: "A/clip.mkv", SizeBytes: 9}, {RelPath: "A/01.flac", SizeBytes: 900}}, 0},
	} {
		if got := importer.Fit(inspect.Unit{Media: tc.files}); got != tc.want {
			t.Errorf("%s: fit %v, want %v", name, got, tc.want)
		}
	}
}
