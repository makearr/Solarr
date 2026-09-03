package score

import (
	"regexp"
	"strings"

	"github.com/makearr/MediaNexus/internal/model"
)

// evaluateScoring is ADR-40 stage 3: the additive, tie-breaking-only score. Every
// rule is checked independently; a candidate can collect any number of them.
func evaluateScoring(rules []ScoringRule, facts Facts, preferred []string) []model.ScoreComponent {
	var components []model.ScoreComponent
	for _, rule := range rules {
		if scoringRuleMatches(rule, facts, preferred) {
			components = append(components, model.ScoreComponent{Rule: rule.Name, Label: rule.Name, Delta: rule.Score})
		}
	}
	return components
}

// scoringRuleMatches follows the *Arr Custom Format model: every required spec
// must match, and at least one non-required spec must too if any exist.
func scoringRuleMatches(rule ScoringRule, facts Facts, preferred []string) bool {
	var optional []Spec
	for _, spec := range rule.Specs {
		if spec.Required {
			if !specMatches(spec, facts, preferred) {
				return false
			}
			continue
		}
		optional = append(optional, spec)
	}
	if len(optional) == 0 {
		return true
	}
	for _, spec := range optional {
		if specMatches(spec, facts, preferred) {
			return true
		}
	}
	return false
}

func specMatches(spec Spec, facts Facts, preferred []string) bool {
	result := evaluateOperator(spec, facts, preferred)
	if spec.Negate {
		return !result
	}
	return result
}

func evaluateOperator(spec Spec, facts Facts, preferred []string) bool {
	switch {
	case spec.In != nil:
		values := spec.In.Values
		if spec.In.Placeholder {
			values = preferred
		}
		got, ok := facts.stringField(spec.Field)
		return ok && containsFold(values, got)
	case spec.Has != "":
		list, ok := facts.listField(spec.Field)
		return ok && containsFold(list, spec.Has)
	case spec.Matches != "":
		got, ok := facts.stringField(spec.Field)
		if !ok {
			return false
		}
		matched, err := regexp.MatchString(spec.Matches, got)
		return err == nil && matched
	case spec.GTE != nil || spec.GT != nil || spec.LTE != nil || spec.LT != nil:
		got, ok := facts.numberField(spec.Field)
		if !ok {
			return false
		}
		if spec.GTE != nil && !(got >= *spec.GTE) {
			return false
		}
		if spec.GT != nil && !(got > *spec.GT) {
			return false
		}
		if spec.LTE != nil && !(got <= *spec.LTE) {
			return false
		}
		if spec.LT != nil && !(got < *spec.LT) {
			return false
		}
		return true
	case spec.Eq != nil:
		got, ok := facts.stringField(spec.Field)
		return ok && strings.EqualFold(got, *spec.Eq)
	case spec.Ne != nil:
		got, ok := facts.stringField(spec.Field)
		return ok && !strings.EqualFold(got, *spec.Ne)
	default:
		return false
	}
}
