package score

import (
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/identify/model"
)

// Fits reports whether facts are among the best a profile asks for: every gate passes, every
// tiered dimension is in its first tier, and every measured target value is within its min and
// max. A library's target is checked against its first profile this way, and a file meets a
// cutoff when it fits a profile at or before it. The reason names the first thing that misses.
func Fits(profile Profile, facts Facts) (bool, string) {
	decision := Evaluate(profile, facts, nil)
	if decision.Outcome != model.OutcomeAccept {
		return false, "its rules refuse it"
	}
	for _, tier := range decision.Quality.Tiers {
		if _, listed := profile.Tiers[tier.Dimension]; listed && tier.Rank > 0 {
			return false, fmt.Sprintf("it prefers another %s to %s", strings.ReplaceAll(tier.Dimension, "_", " "), tier.Label)
		}
	}
	for dimension, resolved := range resolveTargets(profile.Targets, facts) {
		if !resolved.hasTarget {
			continue
		}
		measured, present := facts.numberField(measuredFieldFor(dimension))
		if !present || measured <= 0 {
			continue
		}
		if outside, bound := outsideRange(dimension, resolved.target, measured); outside {
			return false, fmt.Sprintf("its %s range is %s", strings.ReplaceAll(dimension, "_", " "), bound)
		}
	}
	return true, ""
}

func outsideRange(dimension string, target Target, measured float64) (bool, string) {
	bound := target.Min + " to " + target.Max
	if target.Min != "" {
		if minimum, err := parseTargetValue(dimension, target.Min); err == nil && measured < minimum {
			return true, bound
		}
	}
	if target.Max != "" {
		if maximum, err := parseTargetValue(dimension, target.Max); err == nil && measured > maximum {
			return true, bound
		}
	}
	return false, ""
}
