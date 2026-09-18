package score

import "github.com/makearr/Solarr/internal/identify/model"

// LanguagePolicy is section 19.8's axis: required is a hard gate, preferred an
// additive score, both any_of not all_of -- mirrors settings.LanguageProfile's shape.
type LanguagePolicy struct {
	AudioRequired     []string
	AudioPreferred    []string
	SubtitleRequired  []string
	SubtitlePreferred []string
	// PreferOriginalAudio and RejectDubbed are carried for forward compatibility but
	// not evaluated: both need an "original language" fact this package lacks input for.
	PreferOriginalAudio bool
	RejectDubbed        bool
}

// languagePreferenceBonus stays well under a quality tier step, so a language
// preference only ever breaks a tie the quality tiers left open.
const languagePreferenceBonus = 2.0

// EvaluateLanguage is section 19.8's axis, kept independent of quality scoring so
// each is testable on its own; EvaluateWithLanguage composes them into one Decision.
func EvaluateLanguage(policy LanguagePolicy, presentAudio, presentSubtitle []string) ([]model.GateResult, []model.ScoreComponent) {
	var gates []model.GateResult
	var components []model.ScoreComponent

	if len(policy.AudioRequired) > 0 {
		gates = append(gates, languageAnyOfGate("audio_language", policy.AudioRequired, presentAudio))
	}
	if len(policy.SubtitleRequired) > 0 {
		gates = append(gates, languageAnyOfGate("subtitle_language", policy.SubtitleRequired, presentSubtitle))
	}

	for _, language := range policy.AudioPreferred {
		if containsFold(presentAudio, language) {
			components = append(components, model.ScoreComponent{
				Rule: "preferred-audio-language", Label: "Preferred audio language: " + language, Delta: languagePreferenceBonus,
			})
		}
	}
	for _, language := range policy.SubtitlePreferred {
		if containsFold(presentSubtitle, language) {
			components = append(components, model.ScoreComponent{
				Rule: "preferred-subtitle-language", Label: "Preferred subtitle language: " + language, Delta: languagePreferenceBonus,
			})
		}
	}
	return gates, components
}

func languageAnyOfGate(name string, want []string, present []string) model.GateResult {
	outcome := requireAnyOfGate(name, want, present)
	return outcome.result
}
