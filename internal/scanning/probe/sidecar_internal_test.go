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
		"Show - S01E01.mkv", "Show - S01E01.eng.srt", "Show - S01E01.de.forced.srt", "Show - S01E01.srt", "Show - S01E02.eng.srt", "Show - S01E01.nfo",
	} {
		file, err := fsys.Create("/tv/Show/Season 1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	got := sidecarSubtitleLanguages(fsys, "/tv/Show/Season 1/Show - S01E01.mkv")
	slices.Sort(got)
	if !slices.Equal(got, []string{"de", "en", "und"}) {
		t.Errorf("sidecar languages = %v, want de, en and und (no language in the name)", got)
	}
}

func TestResolutionOf_NamesAPictureByItsLines(t *testing.T) {
	cases := []struct {
		width, height int
		want          string
	}{{3840, 2160, "2160p"}, {3840, 1600, "2160p"}, {1920, 1080, "1080p"}, {1920, 800, "1080p"}, {1280, 720, "720p"}, {720, 576, "576p"}, {720, 480, "480p"}, {640, 360, "360p"}}
	for _, c := range cases {
		if got := resolutionOf(c.width, c.height); got != c.want {
			t.Errorf("resolutionOf(%d, %d) = %q, want %q", c.width, c.height, got, c.want)
		}
	}
}
