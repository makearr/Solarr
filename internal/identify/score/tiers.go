package score

import (
	"fmt"
	"math"
	"strings"

	"github.com/makearr/MediaNexus/internal/identify/model"
)

// rankInGroups finds which tier group a value falls in, case-insensitively. found
// is false when unlisted, and rank falls back to len(groups), worse than every tier.
func rankInGroups(groups [][]string, value string) (rank int, found bool) {
	for index, group := range groups {
		if containsFold(group, value) {
			return index, true
		}
	}
	return len(groups), false
}

// buildQualityVector builds one TierValue per compare dimension, in order -- proof
// of ADR-40's "no change to the interpreter": a video and a music profile run this same code.
func buildQualityVector(compare []string, tiers map[string][][]string, facts Facts, distances map[string]targetResult) model.QualityVector {
	vector := model.QualityVector{}
	for _, dimension := range compare {
		vector.Tiers = append(vector.Tiers, tierValueFor(dimension, tiers, facts, distances))
	}
	return vector
}

func tierValueFor(dimension string, tiers map[string][][]string, facts Facts, distances map[string]targetResult) model.TierValue {
	if dimension == "score" {
		// The additive stage breaks ties outside model.Compare (Decision.Score),
		// so as a comparison dimension it always agrees.
		return model.TierValue{Dimension: dimension, Rank: 0, Label: "n/a", Detail: "tie-break is the additive score stage, not a tier"}
	}
	if base, isDistance := strings.CutSuffix(dimension, "_distance"); isDistance {
		return distanceTier(dimension, distances[base])
	}

	if groups, declared := tiers[dimension]; declared {
		return listTier(dimension, groups, facts)
	}
	return numericTier(dimension, facts)
}

// distanceTier renders any dimension's own "<dimension>_distance" entry --
// ADR-40 amended 2026-09-04 so this is a lookup, not a dimension-specific
// branch: the interpreter gains no new code per dimension, only the profile's
// own "targets" data does.
func distanceTier(compareName string, result targetResult) model.TierValue {
	if !result.distanceOK {
		return model.TierValue{Dimension: compareName, Rank: math.MaxInt32, Label: "unmeasured", Detail: "no matching target or dimension not probed"}
	}
	// Scaled to an int without losing ordering; distances stay well under this range.
	rank := int(math.Round(result.distance * 1_000_000))
	return model.TierValue{
		Dimension: compareName,
		Rank:      rank,
		Label:     fmt.Sprintf("%.3f", result.distance),
		Detail:    "log2(measured/ideal), weighted by over/under",
	}
}

func listTier(dimension string, groups [][]string, facts Facts) model.TierValue {
	raw, present := facts.stringField(dimension)
	if !present {
		return model.TierValue{Dimension: dimension, Rank: len(groups), Label: "unmeasured", Detail: "field not measured"}
	}
	rank, found := rankInGroups(groups, raw)
	if !found {
		return model.TierValue{Dimension: dimension, Rank: rank, Label: raw, Detail: fmt.Sprintf("%q not listed in any configured tier", raw)}
	}
	return model.TierValue{Dimension: dimension, Rank: rank, Label: raw, Detail: fmt.Sprintf("tier %d", rank)}
}

// numericTier covers dimensions with no tier list: continuous values compared
// directly, higher always better -- true for every such field this project has today.
func numericTier(dimension string, facts Facts) model.TierValue {
	if dimension == "lossless" {
		if facts.File.Lossless {
			return model.TierValue{Dimension: dimension, Rank: 0, Label: "lossless", Detail: "measured lossless"}
		}
		return model.TierValue{Dimension: dimension, Rank: 1, Label: "lossy", Detail: "measured lossy"}
	}

	value, present := facts.numberField(dimension)
	if !present {
		return model.TierValue{Dimension: dimension, Rank: math.MaxInt32, Label: "unmeasured", Detail: "field not measured"}
	}
	// Negated so a larger raw value yields a smaller (better) rank, matching
	// "lower rank wins" without a second comparison direction to configure.
	rank := -int(math.Round(value))
	return model.TierValue{Dimension: dimension, Rank: rank, Label: fmt.Sprintf("%g", value), Detail: "higher is better"}
}
