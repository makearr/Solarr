package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/media/tv"
)

// A release name is read for the source, group and revision an import keeps; a plain folder says none (QUAL-1).
func TestImporter_ReleaseFactsOf(t *testing.T) {
	for title, want := range map[string]struct {
		facts batch.ReleaseFacts
		ok    bool
	}{
		"Show.Name.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP": {batch.ReleaseFacts{Source: "webdl", Group: "GRP", Revision: 1}, true},
		"Show.Name.S01E02.REPACK.1080p.BluRay.x264-GRP":  {batch.ReleaseFacts{Source: "bluray", Group: "GRP", Revision: 2}, true},
		"Show Name": {batch.ReleaseFacts{}, false},
	} {
		got, ok := tv.Importer{}.ReleaseFactsOf(title)
		if ok != want.ok || got != want.facts {
			t.Errorf("%q = %+v, %v; want %+v, %v", title, got, ok, want.facts, want.ok)
		}
	}
}
