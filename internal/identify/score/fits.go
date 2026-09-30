package score

import (
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/identify/model"
)

// Fits reports whether facts are among the best a profile asks for: Accepts, and every tiered dimension
// in its first tier. A file meets a cutoff when it fits a profile at or before it.
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
	return withinTargets(profile, facts)
}

// Accepts reports whether a profile takes facts at all: its rules accept them, in any tier, and every
// measured value lies within its target's min and max. A library's target need only be accepted.
func Accepts(profile Profile, facts Facts) (bool, string) {
	if Evaluate(profile, facts, nil).Outcome != model.OutcomeAccept {
		return false, "its rules refuse it"
	}
	return withinTargets(profile, facts)
}

func withinTargets(profile Profile, facts Facts) (bool, string) {
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
