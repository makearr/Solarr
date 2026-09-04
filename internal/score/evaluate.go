package score

import "github.com/makearr/MediaNexus/internal/model"

// Evaluate runs ADR-40's three stages against one candidate. preferred substitutes
// a scoring spec's "$preferred" placeholder, e.g. release groups the user favours.
func Evaluate(profile Profile, facts Facts, preferred []string) model.Decision {
	distances := resolveTargets(profile.Targets, facts)
	bitrate := distances["bitrate"]

	gates, rejections, deferrals, outcome := evaluateGates(profile.Require, profile.Tiers, bitrate.target, bitrate.hasTarget, facts)
	vector := buildQualityVector(profile.Compare, profile.Tiers, facts, distances)
	components := evaluateScoring(profile.Scoring, facts, preferred)

	return model.Decision{
		Outcome:    outcome,
		Gates:      gates,
		Quality:    vector,
		Components: components,
		Rejections: rejections,
		Deferrals:  deferrals,
	}
}

// EvaluateWithLanguage assembles one Decision from Evaluate and EvaluateLanguage --
// outcome is the worse of the two, REJECT beats DEFER beats ACCEPT.
func EvaluateWithLanguage(profile Profile, facts Facts, preferred []string, policy LanguagePolicy, presentAudio, presentSubtitle []string) model.Decision {
	decision := Evaluate(profile, facts, preferred)
	languageGates, languageComponents := EvaluateLanguage(policy, presentAudio, presentSubtitle)

	decision.Gates = append(decision.Gates, languageGates...)
	decision.Components = append(decision.Components, languageComponents...)

	for _, gate := range languageGates {
		if gate.Passed {
			continue
		}
		if gate.Got == unmeasuredFact {
			decision.Deferrals = append(decision.Deferrals, model.Deferral{Reason: "unmeasured:" + gate.Gate})
			if decision.Outcome == model.OutcomeAccept {
				decision.Outcome = model.OutcomeDefer
			}
			continue
		}
		decision.Rejections = append(decision.Rejections, model.Rejection{Rule: gate.Gate, Field: gate.Gate, Wanted: gate.Wanted, Got: gate.Got})
		decision.Outcome = model.OutcomeReject
	}
	return decision
}
