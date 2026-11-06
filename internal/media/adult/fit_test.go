package adult

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

func TestImporter_Fit(t *testing.T) {
	importer := NewImporter(ImporterConfig{})
	for name, tc := range map[string]struct {
		file string
		want float64
	}{
		"a release saying XXX": {"Studio.20.05.14.Title.XXX.1080p.mp4", batch.FitDecisive},
		"a scene named by day": {"[Studio] Title (2020-05-14).mp4", batch.FitCertain},
		"a film named by year": {"Film (2001).mkv", 0},
		"sound":                {"01.flac", 0},
	} {
		if got := importer.Fit(inspect.Unit{Media: []inspect.File{{RelPath: tc.file, SizeBytes: 900}}}); got != tc.want {
			t.Errorf("%s: fit %v, want %v", name, got, tc.want)
		}
	}
}
