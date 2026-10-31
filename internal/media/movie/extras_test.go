package movie

import "testing"

func TestIsExtra(t *testing.T) {
	for title, want := range map[string]bool{
		"Jaws.1975.720p.BluRay.x264-x0r[EXTRA-The.Making.of.Jaws": true,
		"Jaws.1975.Featurette.Shark.Tales.1080p":                  true,
		"Heat.1995.Behind.the.Scenes.720p":                        true,
		"Jaws.1975.1080p.BluRay.x265-LAMA":                        false,
		"Trailer.Park.Boys.The.Movie.2006.720p.WEB":               false,
		"Extras.2005.DVDRip":                                      false,
	} {
		if got := isExtra(title); got != want {
			t.Errorf("isExtra(%q) = %v, want %v", title, got, want)
		}
	}
}
