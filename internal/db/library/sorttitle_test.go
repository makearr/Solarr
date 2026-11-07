package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestSortTitleDropsALeadingArticle(t *testing.T) {
	for title, want := range map[string]string{"The Example Film": "example film", "A Quiet Hour": "quiet hour", "An Evening": "evening", "Glow": "glow", "  Ember  3 ": "ember 3"} {
		if got := library.SortTitle(title); got != want {
			t.Errorf("library.SortTitle(%q) = %q, want %q", title, got, want)
		}
	}
}
