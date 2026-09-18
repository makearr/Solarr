package match_test

import (
	"math"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/identify/match"
)

func TestAssign_EmptyMatrix(t *testing.T) {
	if got := match.Assign(nil); got != nil {
		t.Errorf("Assign(nil) = %v, want nil", got)
	}
}

// A greedy row-by-row pick takes 0->0 (cost 1) and is then forced into 1->1 (cost 100).
func TestAssign_BeatsTheGreedyPick(t *testing.T) {
	got := match.Assign([][]float64{
		{1, 2},
		{2, 100},
	})
	if want := []int{1, 0}; !slices.Equal(got, want) {
		t.Errorf("Assign = %v, want %v", got, want)
	}
}

// Every permutation of a 6x6 matrix is checked, so the answer is provably optimal.
func TestAssign_MatchesBruteForceOnAFixedMatrix(t *testing.T) {
	cost := [][]float64{
		{0.9, 0.2, 0.7, 0.4, 0.8, 0.3},
		{0.1, 0.8, 0.6, 0.9, 0.5, 0.7},
		{0.6, 0.4, 0.1, 0.8, 0.9, 0.2},
		{0.7, 0.9, 0.3, 0.2, 0.6, 0.8},
		{0.4, 0.5, 0.9, 0.7, 0.1, 0.6},
		{0.8, 0.3, 0.5, 0.6, 0.7, 0.4},
	}
	got := total(cost, match.Assign(cost))
	if best := bruteForceMinimum(cost); math.Abs(got-best) > 1e-9 {
		t.Errorf("assignment cost = %v, want the brute-force minimum %v", got, best)
	}
}

func total(cost [][]float64, assignment []int) float64 {
	sum := 0.0
	for row, column := range assignment {
		sum += cost[row][column]
	}
	return sum
}

func bruteForceMinimum(cost [][]float64) float64 {
	columns := make([]int, len(cost))
	for i := range columns {
		columns[i] = i
	}
	best := math.Inf(1)
	var permute func(k int)
	permute = func(k int) {
		if k == len(columns) {
			best = min(best, total(cost, columns))
			return
		}
		for i := k; i < len(columns); i++ {
			columns[k], columns[i] = columns[i], columns[k]
			permute(k + 1)
			columns[k], columns[i] = columns[i], columns[k]
		}
	}
	permute(0)
	return best
}
