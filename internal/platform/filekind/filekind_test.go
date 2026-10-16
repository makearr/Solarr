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

func TestIsFeatureLeavesSamplesTrailersAndExtrasOut(t *testing.T) {
	for relPath, want := range map[string]bool{
		"Heat (1995)/Heat (1995).mkv":         true,
		"Show/Season 1/Show - S01E01.mp4":     true,
		"Heat (1995)/Extras/Making of.mkv":    false,
		"Heat (1995)/Trailers/trailer.mp4":    false,
		"Heat.1995.1080p/sample.mkv":          false,
		"Heat.1995.1080p/Heat-sample.mkv":     false,
		"Heat (1995)/Heat (1995).en.srt":      false,
		"Heat (1995)/cover.jpg":               false,
		"Samplers (2001)/Samplers (2001).mkv": true,
	} {
		if got := filekind.IsFeature(relPath); got != want {
			t.Errorf("IsFeature(%q) = %v, want %v", relPath, got, want)
		}
	}
}
