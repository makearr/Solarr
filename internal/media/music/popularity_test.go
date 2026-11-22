package music_test

import (
	"math"
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
)

func TestPopularityScore_TwoPointsAPowerOfTenCappedAtTen(t *testing.T) {
	for _, row := range []struct {
		people int64
		want   float64
	}{{0, 0}, {-5, 0}, {9, 2}, {999, 6}, {1_000_000, 10}} {
		if got := music.PopularityScore(row.people); math.Abs(got-row.want) > 0.01 {
			t.Errorf("PopularityScore(%d) = %.3f, want %.1f", row.people, got, row.want)
		}
	}
}
