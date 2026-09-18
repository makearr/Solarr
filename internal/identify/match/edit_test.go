package match_test

import (
	"math"
	"testing"

	"github.com/makearr/Solarr/internal/identify/match"
)

func TestEditPenalty(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want float64
	}{
		{"", "", 0},
		{"Sesoge", "", 1},
		{"Kebilegev Bibihi", "kebilegev bibihi", 0},
		{"Café del Mar", "Cafe Del Mar!", 0},
		{"kitten", "sitting", 3.0 / 7},
		{"Voca Pebu a Maha", "Just Like Tepa", 4.0 / 13},
	} {
		if got := match.EditPenalty(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("EditPenalty(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
