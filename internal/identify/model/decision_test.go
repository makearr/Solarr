package model_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/identify/model"
)

func TestDecision_ScoreSumsComponents(t *testing.T) {
	d := model.Decision{Components: []model.ScoreComponent{
		{Rule: "preferred-group", Delta: 10},
		{Rule: "healthy-swarm", Delta: 9},
	}}
	if got, want := d.Score(), 19.0; got != want {
		t.Errorf("Score() = %v, want %v", got, want)
	}
}

func TestCompare_EarlierTierDecides(t *testing.T) {
	a := model.QualityVector{Tiers: []model.TierValue{{Dimension: "resolution", Rank: 0}, {Dimension: "codec", Rank: 5}}}
	b := model.QualityVector{Tiers: []model.TierValue{{Dimension: "resolution", Rank: 1}, {Dimension: "codec", Rank: 0}}}

	winner, decidedAt := model.Compare(a, b)
	if winner != -1 || decidedAt != 0 {
		t.Errorf("Compare(a, b) = (%d, %d), want (-1, 0): resolution decides before codec is ever consulted",
			winner, decidedAt)
	}
}

func TestCompare_FallsThroughToALaterTierWhenEarlierOnesTie(t *testing.T) {
	a := model.QualityVector{Tiers: []model.TierValue{{Rank: 0}, {Rank: 2}}}
	b := model.QualityVector{Tiers: []model.TierValue{{Rank: 0}, {Rank: 1}}}

	winner, decidedAt := model.Compare(a, b)
	if winner != 1 || decidedAt != 1 {
		t.Errorf("Compare(a, b) = (%d, %d), want (1, 1)", winner, decidedAt)
	}
}

func TestCompare_EqualThroughoutDecidesNothing(t *testing.T) {
	a := model.QualityVector{Tiers: []model.TierValue{{Rank: 3}}}
	b := model.QualityVector{Tiers: []model.TierValue{{Rank: 3}}}

	winner, decidedAt := model.Compare(a, b)
	if winner != 0 || decidedAt != -1 {
		t.Errorf("Compare(a, b) = (%d, %d), want (0, -1): score is the only thing left to break the tie",
			winner, decidedAt)
	}
}
