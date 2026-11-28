package adult

import "testing"

func TestKindsAreUniqueAndCoverTheGenres(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range kinds() {
		if seen[kind.Key] {
			t.Errorf("kind %q listed twice", kind.Key)
		}
		seen[kind.Key] = true
	}
	for _, key := range []string{"jav", "amateur", "pmv", "bts", "hentai_animation", "live_streams", "vr"} {
		if !seen[key] {
			t.Errorf("kind %q missing", key)
		}
	}
}
