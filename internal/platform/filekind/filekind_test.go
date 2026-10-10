package filekind_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/filekind"
)

func TestOf(t *testing.T) {
	for relPath, want := range map[string]filekind.Kind{
		"Artist/Album/01 - Song.FLAC":               filekind.Audio,
		"Show/Season 01/Show - S01E01.mkv":          filekind.Video,
		"Show/Season 01/Show - S01E01.en.srt":       filekind.Subtitle,
		"Artist/Album/cover.JPG":                    filekind.Image,
		"Artist/Album/album.cue":                    filekind.Other,
		"Show/Season 01/Show - S01E01.mkv.partial~": filekind.Other,
		"README": filekind.Other,
	} {
		if got := filekind.Of(relPath); got != want {
			t.Errorf("Of(%q) = %q, want %q", relPath, got, want)
		}
	}
}
