package movie_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media/movie"
)

func TestAvailableOnFollowsTheMinimumAvailability(t *testing.T) {
	m := movie.Movie{InCinemas: "2024-03-01", Digital: "2024-05-10", Physical: "2024-06-01"}
	for availability, want := range map[movie.Availability]string{
		movie.AvailableAnnounced: "",
		movie.AvailableInCinemas: "2024-03-01",
		movie.AvailableReleased:  "2024-05-10",
	} {
		if got := m.AvailableOn(availability); got != want {
			t.Errorf("%s = %q, want %q", availability, got, want)
		}
	}
	noHome := movie.Movie{InCinemas: "2024-03-01"}
	if got := noHome.AvailableOn(movie.AvailableReleased); got != "2024-05-30" {
		t.Errorf("no home release: %q, want the cinema day plus 90 days", got)
	}
	if got := (movie.Movie{}).AvailableOn(movie.AvailableReleased); got != "" {
		t.Errorf("no dates at all: %q, want none", got)
	}
}

func TestStatusOnReadsTheDates(t *testing.T) {
	now := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		movie movie.Movie
		want  movie.Status
	}{
		"ahead":      {movie.Movie{InCinemas: "2024-08-01"}, movie.StatusAnnounced},
		"in cinemas": {movie.Movie{InCinemas: "2024-03-01", Digital: "2024-07-01"}, movie.StatusInCinemas},
		"released":   {movie.Movie{InCinemas: "2024-01-01", Digital: "2024-03-01"}, movie.StatusReleased},
		"nothing":    {movie.Movie{}, movie.StatusAnnounced},
	}
	for name, c := range cases {
		if got := c.movie.StatusOn(now); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestSortTitleDropsALeadingArticle(t *testing.T) {
	for title, want := range map[string]string{"The Tonhut": "matrix", "A Quiet Place": "quiet place", "An Education": "education", "Heat": "heat", "  Ihaol  3 ": "alien 3"} {
		if got := movie.SortTitle(title); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", title, got, want)
		}
	}
}
