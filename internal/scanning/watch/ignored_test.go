package watch

import "testing"

// Temp names carry a random suffix, so a suffix test never matched one.
func TestIgnoredName(t *testing.T) {
	for name, want := range map[string]bool{
		".mnx-move-tmp0a1b2c3d": true, ".solarr-link-tmp0a1b": true, ".solarr-probe0a1b": true,
		".mnx-write-probe-0a1b": true, "movie.mkv.partial": true, "01 Song.flac": false, "tmp.flac": false,
	} {
		if got := ignoredName(name); got != want {
			t.Errorf("ignoredName(%q) = %v, want %v", name, got, want)
		}
	}
}
