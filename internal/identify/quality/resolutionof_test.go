package quality

import "testing"

func TestResolutionOf_NamesAPictureByItsLines(t *testing.T) {
	cases := []struct {
		width, height int
		want          string
	}{{3840, 2160, "2160p"}, {3840, 1600, "2160p"}, {1920, 1080, "1080p"}, {1920, 800, "1080p"}, {1280, 720, "720p"}, {720, 576, "576p"}, {720, 480, "480p"}, {640, 360, "360p"}}
	for _, c := range cases {
		if got := ResolutionOf(c.width, c.height); got != c.want {
			t.Errorf("ResolutionOf(%d, %d) = %q, want %q", c.width, c.height, got, c.want)
		}
	}
}
