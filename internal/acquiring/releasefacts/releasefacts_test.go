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

// What the grab recorded outranks the library file's name, which a naming scheme may have stripped (QUAL-1).
func TestFromFile_PrefersWhatTheGrabRecorded(t *testing.T) {
	file := library.EpisodeFile{
		RelPath:       "Show/Season 01/Show - S01E01 - Title.mkv",
		ReleaseSource: "webdl", ReleaseGroup: "GRP", Revision: 2,
	}
	got := releasefacts.FromFile(file).File
	if got.Source != "webdl" || got.ReleaseGroup != "GRP" || !got.ProperRepack || !got.ReleaseKnown {
		t.Errorf("facts = %+v, want webdl, GRP, a repack, known", got)
	}
	scanned := releasefacts.FromFile(library.EpisodeFile{RelPath: "Show/Show.S01E01.1080p.BluRay.mkv"}).File
	if scanned.Source != "bluray" || scanned.ReleaseKnown || scanned.ReleaseGroup != "" {
		t.Errorf("a scanned file's facts = %+v, want the name's bluray and nothing recorded", scanned)
	}
}
