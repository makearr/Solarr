package screen_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/screen"
)

// Every certification the real library holds reads as an age or as unrated, US letters and bare numbers alike.
func TestRatingAge_ReadsTheCertificationsProvidersGive(t *testing.T) {
	for rating, want := range map[string]int64{
		"G": 0, "PG": 7, "PG-13": 13, "R": 17, "NC-17": 18, "TV-Y": 0, "TV-Y7": 7, "TV-PG": 10, "TV-14": 14, "TV-MA": 17,
		"12": 12, "16+": 16, "+16": 16, "-12": 12, "18+": 18, "0": 0, "MA 15+": 15, "14A": 14, "TP": 0, "l": 0, "R21": 21,
	} {
		if got, ok := screen.RatingAge(rating); !ok || got != want {
			t.Errorf("RatingAge(%q) = %d, %v; want %d", rating, got, ok, want)
		}
	}
	for _, rating := range []string{"NR", "", "IIA", "99"} {
		if age, ok := screen.RatingAge(rating); ok {
			t.Errorf("RatingAge(%q) = %d, want unrated", rating, age)
		}
	}
}
