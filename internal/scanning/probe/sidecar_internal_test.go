package probe

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestSidecarSubtitleLanguages_ReadsTheLanguageFromTheFileName(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/tv/Show/Season 1", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"Show - S01E01.mkv", "Show - S01E01.eng.srt", "Show - S01E01.de.forced.srt", "Show - S01E01.srt", "Show - S01E01.German.sdh.srt", "Show - S01E02.eng.srt", "Show - S01E01.nfo",
	} {
		file, err := fsys.Create("/tv/Show/Season 1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	got := sidecarSubtitleLanguages(fsys, "/tv/Show/Season 1/Show - S01E01.mkv")
	slices.Sort(got)
	if !slices.Equal(got, []string{"de", "de", "en", "und"}) {
		t.Errorf("sidecar languages = %v, want de (twice), en and und (no language in the name)", got)
	}
}
