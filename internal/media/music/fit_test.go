package music_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/music"
)

func TestImporter_Fit(t *testing.T) {
	importer := music.NewImporter(music.ImporterConfig{})
	for name, tc := range map[string]struct {
		files []string
		want  float64
	}{
		"an album":                {[]string{"A/01.flac", "A/02.mp3"}, batch.FitLikely},
		"an audiobook's own file": {[]string{"B/book.m4b"}, batch.FitPossible},
		"a video among the sound": {[]string{"A/01.flac", "A/clip.mkv"}, 0},
	} {
		unit := inspect.Unit{}
		for _, file := range tc.files {
			unit.Media = append(unit.Media, inspect.File{RelPath: file})
		}
		if got := importer.Fit(unit); got != tc.want {
			t.Errorf("%s: fit %v, want %v", name, got, tc.want)
		}
	}
}
