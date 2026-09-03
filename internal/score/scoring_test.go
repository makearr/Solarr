package score

import "testing"

func float64Ptr(value float64) *float64 { return &value }
func stringPtr(value string) *string    { return &value }

func TestScoringRuleMatches_AllRequiredMustMatch(t *testing.T) {
	rule := ScoringRule{
		Specs: []Spec{
			{Field: "edition_tokens", Has: "remux", Required: true},
			{Field: "audio_codec", Eq: stringPtr("dts"), Required: true},
		},
	}
	facts := Facts{File: FileFacts{EditionTokens: []string{"remux"}, AudioCodec: "aac"}}
	if scoringRuleMatches(rule, facts, nil) {
		t.Error("scoringRuleMatches = true, want false (one required spec fails)")
	}
}

func TestScoringRuleMatches_NonRequiredIsAnyOf(t *testing.T) {
	rule := ScoringRule{
		Specs: []Spec{
			{Field: "seeders", GTE: float64Ptr(20)},
			{Field: "protocol", Eq: stringPtr("usenet")},
		},
	}
	facts := Facts{Release: ReleaseFacts{Seeders: 5, Protocol: "usenet"}}
	if !scoringRuleMatches(rule, facts, nil) {
		t.Error("scoringRuleMatches = false, want true (one of two non-required specs matches)")
	}
}

func TestScoringRuleMatches_ZeroNonRequiredSpecsIsVacuouslyTrue(t *testing.T) {
	rule := ScoringRule{Specs: []Spec{{Field: "audio_codec", Eq: stringPtr("dts"), Required: true}}}
	facts := Facts{File: FileFacts{AudioCodec: "dts"}}
	if !scoringRuleMatches(rule, facts, nil) {
		t.Error("scoringRuleMatches = false, want true (all required specs matched, no optional specs to fail)")
	}
}

func TestSpecMatches_NegateInvertsTheOperatorResult(t *testing.T) {
	spec := Spec{Field: "title", Matches: "(?i)upscal", Negate: true}
	if specMatches(spec, Facts{Release: ReleaseFacts{Title: "Movie 2160p Upscaled"}}, nil) {
		t.Error("specMatches = true, want false (negated match on a title that does match)")
	}
	if !specMatches(spec, Facts{Release: ReleaseFacts{Title: "Movie 2160p Remux"}}, nil) {
		t.Error("specMatches = false, want true (negated match on a title that does not match)")
	}
}

func TestSpecMatches_InPlaceholderUsesCallerPreferredList(t *testing.T) {
	spec := Spec{Field: "release_group", In: &InClause{Placeholder: true}}
	facts := Facts{File: FileFacts{ReleaseGroup: "GROUPX"}}
	if specMatches(spec, facts, []string{"GROUPX"}) != true {
		t.Error("specMatches = false, want true (release group is in the caller's preferred list)")
	}
	if specMatches(spec, facts, []string{"OTHER"}) != false {
		t.Error("specMatches = true, want false (release group is not in the caller's preferred list)")
	}
}

func TestSpecMatches_InLiteralList(t *testing.T) {
	spec := Spec{Field: "audio_codec", In: &InClause{Values: []string{"dts", "truehd"}}}
	facts := Facts{File: FileFacts{AudioCodec: "TrueHD"}}
	if !specMatches(spec, facts, nil) {
		t.Error("specMatches = false, want true (case-insensitive match against a literal in-list)")
	}
}

func TestSpecMatches_Has(t *testing.T) {
	spec := Spec{Field: "edition_tokens", Has: "remux"}
	if !specMatches(spec, Facts{File: FileFacts{EditionTokens: []string{"remux", "extended"}}}, nil) {
		t.Error("specMatches = false, want true")
	}
	if specMatches(spec, Facts{File: FileFacts{EditionTokens: []string{"extended"}}}, nil) {
		t.Error("specMatches = true, want false")
	}
}

func TestSpecMatches_NumericComparisons(t *testing.T) {
	facts := Facts{Release: ReleaseFacts{Seeders: 20}}
	if !specMatches(Spec{Field: "seeders", GTE: float64Ptr(20)}, facts, nil) {
		t.Error("gte 20 against seeders=20 should match")
	}
	if specMatches(Spec{Field: "seeders", GT: float64Ptr(20)}, facts, nil) {
		t.Error("gt 20 against seeders=20 should not match")
	}
	if !specMatches(Spec{Field: "seeders", LTE: float64Ptr(20)}, facts, nil) {
		t.Error("lte 20 against seeders=20 should match")
	}
	if specMatches(Spec{Field: "seeders", LT: float64Ptr(20)}, facts, nil) {
		t.Error("lt 20 against seeders=20 should not match")
	}
}

func TestSpecMatches_MissingFieldNeverMatches(t *testing.T) {
	spec := Spec{Field: "seeders", GTE: float64Ptr(1)}
	if specMatches(spec, Facts{}, nil) {
		t.Error("specMatches = true, want false for a field with no data")
	}
}

func TestEvaluateScoring_SumsEveryMatchingRule(t *testing.T) {
	rules := []ScoringRule{
		{Name: "a", Score: 10, Specs: []Spec{{Field: "audio_codec", Eq: stringPtr("dts"), Required: true}}},
		{Name: "b", Score: 5, Specs: []Spec{{Field: "audio_codec", Eq: stringPtr("aac"), Required: true}}},
	}
	facts := Facts{File: FileFacts{AudioCodec: "dts"}}
	components := evaluateScoring(rules, facts, nil)
	if len(components) != 1 || components[0].Rule != "a" || components[0].Delta != 10 {
		t.Errorf("components = %+v, want exactly rule a with delta 10", components)
	}
}
