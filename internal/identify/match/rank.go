package match

import (
	"cmp"
	"slices"
)

// Thresholds are section 10.2's two acceptance gates: MaxDistance is the ceiling
// a match must clear, AmbiguityMargin the gap the winner must hold over the
// runner-up. Both configurable per media type, like Weights.
type Thresholds struct {
	MaxDistance     float64
	AmbiguityMargin float64
}

// RankedCandidate pairs a candidate's opaque id with its distance. ID is the
// caller's own key (a work id, an edition id) -- this package never interprets
// it, only carries it through from Candidate.
type RankedCandidate struct {
	ID       string
	Distance Distance
}

// Result is Rank's answer. Candidates is always populated, best (lowest total
// distance) first, so a manual-identification-queue UI has everything it needs
// whether or not Best resolved -- Best is nil exactly when nothing cleared both
// thresholds, the same "do not guess" shape as model.ChainResult.
type Result struct {
	Best       *RankedCandidate
	Candidates []RankedCandidate
}

// Rank scores claimed against every candidate and decides whether the best one
// is confident enough to resolve automatically: its distance must clear
// MaxDistance, and beat the runner-up by more than AmbiguityMargin. A single
// candidate still has to clear MaxDistance -- being the only option considered
// is not evidence on its own.
func Rank(claimed Claimed, candidates []Candidate, weights Weights, thresholds Thresholds) Result {
	if len(candidates) == 0 {
		return Result{}
	}

	ranked := make([]RankedCandidate, len(candidates))
	for i, candidate := range candidates {
		ranked[i] = RankedCandidate{ID: candidate.ID, Distance: Compute(claimed, candidate, weights)}
	}
	slices.SortFunc(ranked, func(a, b RankedCandidate) int {
		return cmp.Compare(a.Distance.Total, b.Distance.Total)
	})

	result := Result{Candidates: ranked}
	if ranked[0].Distance.Total > thresholds.MaxDistance {
		return result
	}
	if len(ranked) == 1 || ranked[1].Distance.Total-ranked[0].Distance.Total > thresholds.AmbiguityMargin {
		result.Best = &ranked[0]
	}
	return result
}
