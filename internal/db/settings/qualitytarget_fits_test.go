package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestQualityTargetFitsMediaType(t *testing.T) {
	epub := settings.QualityTarget{MediaType: "book", Container: "epub"}
	cbz := settings.QualityTarget{MediaType: "book", Container: "cbz"}
	video := settings.QualityTarget{MediaType: "video"}
	cases := []struct {
		name      string
		target    settings.QualityTarget
		mediaType string
		want      bool
	}{
		{"epub to books", epub, "book", true},
		{"cbz not to books", cbz, "book", false},
		{"cbz to comics", cbz, "comic", true},
		{"epub not to comics", epub, "comic", false},
		{"video to tv", video, "tv", true},
		{"video not to books", video, "book", false},
	}
	for _, c := range cases {
		if got := c.target.FitsMediaType(c.mediaType); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
