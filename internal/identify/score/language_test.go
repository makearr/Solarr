package score

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/model"
)

func TestEvaluateLanguage_EmptyPolicyMeansNoConstraint(t *testing.T) {
	gates, components := EvaluateLanguage(LanguagePolicy{}, nil, nil)
	if len(gates) != 0 || len(components) != 0 {
		t.Errorf("gates=%v components=%v, want both empty for an empty policy", gates, components)
	}
}

func TestEvaluateLanguage_AnyOfSemantics(t *testing.T) {
	policy := LanguagePolicy{AudioRequired: []string{"en", "de"}}

	gates, _ := EvaluateLanguage(policy, []string{"en"}, nil)
	if len(gates) != 1 || !gates[0].Passed {
		t.Errorf("English-only audio against require [en de]: gates=%+v, want one passing gate", gates)
	}

	gates, _ = EvaluateLanguage(policy, []string{"de"}, nil)
	if len(gates) != 1 || !gates[0].Passed {
		t.Errorf("German-only audio against require [en de]: gates=%+v, want one passing gate", gates)
	}

	gates, _ = EvaluateLanguage(policy, []string{"ru"}, nil)
	if len(gates) != 1 || gates[0].Passed {
		t.Errorf("Russian-only audio against require [en de]: gates=%+v, want one failing gate", gates)
	}
}

func TestEvaluateLanguage_UnmeasuredMarksTheGateDistinctly(t *testing.T) {
	gates, _ := EvaluateLanguage(LanguagePolicy{AudioRequired: []string{"en"}}, nil, nil)
	if len(gates) != 1 || gates[0].Got != unmeasuredFact {
		t.Errorf("gates = %+v, want one gate with Got=%q", gates, unmeasuredFact)
	}
}

func TestEvaluateLanguage_PreferredContributesASmallBonusPerLanguage(t *testing.T) {
	policy := LanguagePolicy{AudioPreferred: []string{"en", "ja"}}
	_, components := EvaluateLanguage(policy, []string{"en"}, nil)
	if len(components) != 1 || components[0].Delta != languagePreferenceBonus {
		t.Errorf("components = %+v, want exactly one component of delta %v", components, languagePreferenceBonus)
	}
}

func TestEvaluateWithLanguage_LanguageRejectionOverridesQualityAccept(t *testing.T) {
	profile := Profile{}
	facts := Facts{}
	policy := LanguagePolicy{AudioRequired: []string{"en"}}

	decision := EvaluateWithLanguage(profile, facts, nil, policy, []string{"ru"}, nil)
	if decision.Outcome != model.OutcomeReject {
		t.Errorf("Outcome = %v, want REJECT", decision.Outcome)
	}
	if len(decision.Rejections) != 1 {
		t.Errorf("Rejections = %v, want exactly one", decision.Rejections)
	}
}

func TestEvaluateWithLanguage_UnmeasuredLanguageDefersAnAcceptedQualityDecision(t *testing.T) {
	profile := Profile{}
	facts := Facts{}
	policy := LanguagePolicy{AudioRequired: []string{"en"}}

	decision := EvaluateWithLanguage(profile, facts, nil, policy, nil, nil)
	if decision.Outcome != model.OutcomeDefer {
		t.Errorf("Outcome = %v, want DEFER (audio track data simply hasn't been probed)", decision.Outcome)
	}
	if len(decision.Deferrals) != 1 {
		t.Errorf("Deferrals = %v, want exactly one", decision.Deferrals)
	}
}

func TestEvaluateWithLanguage_QualityRejectionSurvivesAPassingLanguageGate(t *testing.T) {
	profile := Profile{Require: Requirements{RejectCodecs: []string{"xvid"}}}
	facts := Facts{File: FileFacts{VideoCodec: "xvid"}}
	policy := LanguagePolicy{AudioRequired: []string{"en"}}

	decision := EvaluateWithLanguage(profile, facts, nil, policy, []string{"en"}, nil)
	if decision.Outcome != model.OutcomeReject {
		t.Errorf("Outcome = %v, want REJECT (quality's own gate failed)", decision.Outcome)
	}
}
