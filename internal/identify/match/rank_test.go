package match

import "testing"

func mustBest(t *testing.T, result Result) RankedCandidate {
	t.Helper()
	if result.Best == nil {
		t.Fatalf("Best = nil, want a confident match. Candidates: %+v", result.Candidates)
	}
	return *result.Best
}

// Two candidates close in total distance must be flagged not confident: the
// ambiguity margin exists exactly to stop a barely-better guess from being
// treated as a resolved identification.
func TestRank_CloseCandidatesAreAmbiguous(t *testing.T) {
	claimed := Claimed{Identity{Title: "Heroes", Year: 1977}}
	candidates := []Candidate{
		{ID: "a", Identity: Identity{Title: "Heroes", Year: 1977}},
		{ID: "b", Identity: Identity{Title: "Heroes", Year: 1978}},
	}

	result := Rank(claimed, candidates, nil, Thresholds{MaxDistance: 1, AmbiguityMargin: 0.1})

	if result.Best != nil {
		t.Errorf("Best = %+v, want nil: candidates too close together to resolve automatically", result.Best)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(result.Candidates))
	}
	if result.Candidates[0].ID != "a" {
		t.Errorf("Candidates[0].ID = %q, want %q (the closer match ranked first)", result.Candidates[0].ID, "a")
	}
}

// A candidate far ahead of the runner-up must resolve confidently -- the
// opposite side of the same ambiguity-margin check.
func TestRank_ClearWinnerIsConfident(t *testing.T) {
	claimed := Claimed{Identity{
		Title:       "Heroes",
		Year:        1977,
		ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-1"}},
	}}
	candidates := []Candidate{
		{ID: "right", Identity: Identity{
			Title: "Heroes", Year: 1977,
			ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-1"}},
		}},
		{ID: "wrong", Identity: Identity{Title: "Completely Different Album", Year: 2003}},
	}

	result := Rank(claimed, candidates, nil, Thresholds{MaxDistance: 0.3, AmbiguityMargin: 0.1})

	best := mustBest(t, result)
	if best.ID != "right" {
		t.Errorf("Best.ID = %q, want %q", best.ID, "right")
	}
}

// A single candidate with no runner-up at all must still respect MaxDistance --
// being the only option is not itself evidence, or a bad match gets accepted
// purely by default.
func TestRank_SingleBadCandidateStillRespectsMaxDistance(t *testing.T) {
	claimed := Claimed{Identity{Title: "Heroes", Year: 1977}}
	candidates := []Candidate{
		{ID: "only", Identity: Identity{Title: "Something Entirely Unrelated", Year: 2020}},
	}

	result := Rank(claimed, candidates, nil, Thresholds{MaxDistance: 0.2, AmbiguityMargin: 0.1})

	if result.Best != nil {
		t.Errorf("Best = %+v, want nil: the only candidate is a bad match and must not be accepted by default", result.Best)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(result.Candidates))
	}
}

// A single candidate that genuinely is a good match must still resolve --
// MaxDistance is a real gate, not a proxy for "more than one candidate."
func TestRank_SingleGoodCandidateResolves(t *testing.T) {
	claimed := Claimed{Identity{Title: "Heroes", Year: 1977}}
	candidates := []Candidate{
		{ID: "only", Identity: Identity{Title: "Heroes", Year: 1977}},
	}

	result := Rank(claimed, candidates, nil, Thresholds{MaxDistance: 0.2, AmbiguityMargin: 0.1})
	best := mustBest(t, result)
	if best.ID != "only" {
		t.Errorf("Best.ID = %q, want %q", best.ID, "only")
	}
}

func TestRank_NoCandidatesGivesEmptyResult(t *testing.T) {
	result := Rank(Claimed{Identity{Title: "Heroes"}}, nil, nil, Thresholds{MaxDistance: 1, AmbiguityMargin: 0})
	if result.Best != nil {
		t.Errorf("Best = %+v, want nil", result.Best)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("got %d candidates, want 0", len(result.Candidates))
	}
}

// Ranking must actually use the overridden weights, not just accept them --
// swapping which dimension dominates should be able to flip which candidate
// Rank considers the confident winner.
func TestRank_WeightOverrideChangesWinner(t *testing.T) {
	claimed := Claimed{Identity{Title: "Greatest Hits", ItemCount: 12}}
	candidates := []Candidate{
		{ID: "title-match", Identity: Identity{Title: "Greatest Hits", ItemCount: 30}},
		{ID: "count-match", Identity: Identity{Title: "Rarities And B-Sides", ItemCount: 12}},
	}
	thresholds := Thresholds{MaxDistance: 1, AmbiguityMargin: 0.01}

	titleHeavy := Rank(claimed, candidates, Weights{DimensionTitle: 10, DimensionTrackCount: 0.1}, thresholds)
	if best := mustBest(t, titleHeavy); best.ID != "title-match" {
		t.Errorf("title-heavy weights: Best.ID = %q, want %q", best.ID, "title-match")
	}

	countHeavy := Rank(claimed, candidates, Weights{DimensionTitle: 0.1, DimensionTrackCount: 10}, thresholds)
	if best := mustBest(t, countHeavy); best.ID != "count-match" {
		t.Errorf("count-heavy weights: Best.ID = %q, want %q", best.ID, "count-match")
	}
}
