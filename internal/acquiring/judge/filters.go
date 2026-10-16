package judge

import (
	"regexp"
	"strings"

	"github.com/makearr/Solarr/internal/identify/model"
)

// Kinds of a work's release filter, as stored (work_release_filter.kind).
const (
	FilterMustContain    = "must_contain"
	FilterMustNotContain = "must_not_contain"
	FilterPrefer         = "prefer"
	FilterGroupAllow     = "group_allow"
	FilterGroupDeny      = "group_deny"
)

// ReleaseFilter is one rule a work puts on the releases offered for it. Prefix narrows it to part of the work ('s03').
type ReleaseFilter struct {
	Prefix  string
	Kind    string
	Value   string
	IsRegex bool
	Score   int
}

// ValidFilterKind reports whether kind is one the judge knows.
func ValidFilterKind(kind string) bool {
	switch kind {
	case FilterMustContain, FilterMustNotContain, FilterPrefer, FilterGroupAllow, FilterGroupDeny:
		return true
	}
	return false
}

// ValidFilter says why a rule cannot be kept, or "".
func ValidFilter(filter ReleaseFilter) string {
	switch {
	case !ValidFilterKind(filter.Kind):
		return "unknown kind " + filter.Kind
	case strings.TrimSpace(filter.Value) == "":
		return "a rule needs a value"
	case filter.IsRegex:
		if _, err := regexp.Compile("(?i)" + filter.Value); err != nil {
			return "not a valid pattern: " + err.Error()
		}
	}
	return ""
}

// ApplyFilters holds a decision against a work's filters: a missing required word, a denied word or group, or a group
// outside the allowed list rejects it; a matching preferred word adds to its score. mediaRef is the part being searched.
func ApplyFilters(decision model.Decision, filters []ReleaseFilter, title, group, mediaRef string) model.Decision {
	var allowed []ReleaseFilter
	var required []ReleaseFilter
	for _, filter := range filters {
		if !strings.HasPrefix(mediaRef, filter.Prefix) {
			continue
		}
		switch filter.Kind {
		case FilterMustContain:
			required = append(required, filter)
		case FilterGroupAllow:
			allowed = append(allowed, filter)
		case FilterMustNotContain:
			if filter.matches(title) {
				decision = rejectBy(decision, "release_filter_denied_word", "none of "+filter.Value, filter.Value)
			}
		case FilterGroupDeny:
			if group != "" && filter.matches(group) {
				decision = rejectBy(decision, "release_filter_denied_group", "not "+filter.Value, group)
			}
		case FilterPrefer:
			if filter.matches(title) {
				decision.Components = append(decision.Components, model.ScoreComponent{
					Rule: "preferred-word", Label: "Preferred word: " + filter.Value, Delta: float64(filter.Score),
				})
			}
		}
	}
	if len(required) > 0 && !anyMatches(required, title) {
		decision = rejectBy(decision, "release_filter_required_word", "any of "+joinValues(required), "none")
	}
	if len(allowed) > 0 && !anyMatches(allowed, group) {
		decision = rejectBy(decision, "release_filter_allowed_group", "one of "+joinValues(allowed), orNone(group))
	}
	return decision
}

func (f ReleaseFilter) matches(text string) bool {
	if f.IsRegex {
		pattern, err := regexp.Compile("(?i)" + f.Value)
		return err == nil && pattern.MatchString(text)
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(f.Value))
}

func anyMatches(filters []ReleaseFilter, text string) bool {
	for _, filter := range filters {
		if text != "" && filter.matches(text) {
			return true
		}
	}
	return false
}

func joinValues(filters []ReleaseFilter) string {
	values := make([]string, len(filters))
	for i, filter := range filters {
		values[i] = filter.Value
	}
	return strings.Join(values, ", ")
}

func orNone(text string) string {
	if text == "" {
		return "no group"
	}
	return text
}

func rejectBy(decision model.Decision, gate, wanted, got string) model.Decision {
	decision.Gates = append(decision.Gates, model.GateResult{Gate: gate, Passed: false, Wanted: wanted, Got: got})
	decision.Rejections = append(decision.Rejections, model.Rejection{Rule: gate, Field: gate, Wanted: wanted, Got: got})
	decision.Outcome = model.OutcomeReject
	return decision
}
