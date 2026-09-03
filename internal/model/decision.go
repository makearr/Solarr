package model

import "time"

// Outcome is what a Decision concluded (ADR-10).
type Outcome string

const (
	// OutcomeAccept means every gate passed; Quality and Components decide ranking.
	OutcomeAccept Outcome = "ACCEPT"
	// OutcomeReject means at least one gate failed. Rejections says which.
	OutcomeReject Outcome = "REJECT"
	// OutcomeDefer means acceptable but not actionable yet -- see Deferrals.
	OutcomeDefer Outcome = "DEFER"
)

// Decision is the output of scoring a candidate against a profile (ADR-10, ADR-40).
// The score is derived from Components, never computed separately, so the
// explanation and the decision cannot drift apart.
type Decision struct {
	Outcome    Outcome
	Gates      []GateResult     // hard requirements, evaluated first (ADR-40 stage 1)
	Quality    QualityVector    // lexicographic tiers (ADR-40 stage 2)
	Components []ScoreComponent // additive; the final tier only (ADR-40 stage 3)
	Rejections []Rejection
	Deferrals  []Deferral
	Confidence float64
}

// Score sums Components. It never orders candidates on its own -- QualityVector
// does that; Score only breaks a tie once the tiers agree (ADR-40).
func (d Decision) Score() float64 {
	var total float64
	for _, component := range d.Components {
		total += component.Delta
	}
	return total
}

// GateResult is one hard requirement, evaluated before any comparison (ADR-40
// stage 1). The first failing gate rejects the candidate, but every gate is still
// evaluated and recorded, so the UI can show everything wrong at once rather than
// only the first thing found.
type GateResult struct {
	Gate   string // "audio_language" | "bitrate_min" | "resolution_range"
	Passed bool
	Wanted string // "any of [en de]"
	Got    string // "ru"
}

// QualityVector is a candidate's position on each dimension a profile's `compare`
// list names, in that order. Comparison walks tiers until one differs, so the
// answer is never a bare number.
type QualityVector struct{ Tiers []TierValue }

// TierValue is one dimension's rank within its own tier list. Equal ranks mean
// the dimension did not decide, and comparison continues to the next one.
type TierValue struct {
	Dimension string // "resolution" | "lossless" | "bitrate_distance" | ...
	Rank      int    // lower is better
	Label     string // "2160p"
	Detail    string // "45.2 Mbit/s, ideal 45.0, log distance 0.01"
}

// Compare returns which of a, b ranks higher and which tier decided it. A result
// of 0 means every tier agreed -- score-only cases, exceedingly rare with a
// reasonable profile. decidedAtTier is -1 when nothing decided (equal throughout).
func Compare(a, b QualityVector) (winner, decidedAtTier int) {
	n := len(a.Tiers)
	if len(b.Tiers) < n {
		n = len(b.Tiers)
	}
	for i := 0; i < n; i++ {
		switch {
		case a.Tiers[i].Rank < b.Tiers[i].Rank:
			return -1, i
		case a.Tiers[i].Rank > b.Tiers[i].Rank:
			return 1, i
		}
	}
	return 0, -1
}

// ScoreComponent is one additive-stage contribution (ADR-40 stage 3, the *Arr
// Custom Format shape). Only ever breaks a tie the tiers left open.
type ScoreComponent struct {
	Rule  string // "preferred-groups"
	Label string // "Preferred release group"
	Delta float64
}

// Rejection is one failed gate, as data rather than a formatted string -- the UI
// formats it, the API can filter on it, and it survives translation (ADR-10).
type Rejection struct{ Rule, Field, Wanted, Got string }

// Deferral is a reason a candidate is not actionable yet, with when to retry.
type Deferral struct {
	Reason     string
	RetryAfter time.Time
	Detail     map[string]any
}
