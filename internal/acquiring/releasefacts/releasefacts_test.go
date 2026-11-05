package releasefacts_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
)

func TestFromFile_SourceComesFromTheFileName(t *testing.T) {
	for name, test := range map[string]struct {
		relPath string
		want    string
	}{
		"web download":   {"Show/Season 08/Show - S08E01 - Title WEBDL-1080p.mkv", "webdl"},
		"release name":   {"Show/Show.S08E01.1080p.BluRay.x264-GRP.mkv", "bluray"},
		"remux":          {"Show/Show.S08E01.1080p.BluRay.REMUX.mkv", "remux"},
		"no claim":       {"Show/Show - S08E01.mkv", ""},
		"no path stored": {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := releasefacts.FromFile(library.EpisodeFile{RelPath: test.relPath}).File.Source
			if got != test.want {
				t.Fatalf("source = %q, want %q", got, test.want)
			}
		})
	}
}
