package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestImporter_Fit(t *testing.T) {
	importer := tv.NewImporter(tv.ImporterConfig{Clock: clock.NewFakeAtEpoch()})
	for name, tc := range map[string]struct {
		files []inspect.File
		want  float64
	}{
		"an episode":                {[]inspect.File{{RelPath: "Show/Show.S01E02.1080p.mkv", SizeBytes: 900}}, batch.FitCertain},
		"a video naming no episode": {[]inspect.File{{RelPath: "Clip/clip.mkv", SizeBytes: 900}}, batch.FitFallback},
		"sound":                     {[]inspect.File{{RelPath: "A/01.flac", SizeBytes: 900}}, 0},
	} {
		if got := importer.Fit(inspect.Unit{Media: tc.files}); got != tc.want {
			t.Errorf("%s: fit %v, want %v", name, got, tc.want)
		}
	}
}
