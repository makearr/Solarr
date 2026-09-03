package apiserver

import (
	"encoding/json"
	"net/http"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/model"
	"github.com/makearr/MediaNexus/internal/parse"
	"github.com/makearr/MediaNexus/internal/score"
)

// maxScoreSimulateBody bounds an authenticated but unbounded-by-nature POST body.
const maxScoreSimulateBody = 1 << 16

// SimulateScore is F6: parse a pasted title, evaluate it against a quality (and
// optionally language) profile, return the Decision. No candidate, no side effects.
func (s *Server) SimulateScore(w http.ResponseWriter, r *http.Request) {
	var request gen.SimulateScoreJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScoreSimulateBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with title and quality_profile_id")
		return
	}
	if request.Title == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "title must not be empty")
		return
	}

	snapshot := s.auth.Settings.Snapshot()
	qualityProfile, found := snapshot.QualityProfile(request.QualityProfileId)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
			"Not found", "quality_profile_id does not exist")
		return
	}
	rules, err := score.Parse([]byte(qualityProfile.RulesJSON))
	if err != nil {
		// Already validated on write (ADR-44); a stored profile that no longer
		// parses is a data problem, not a request problem.
		writeError(w, s.logger, err)
		return
	}

	var languagePolicy score.LanguagePolicy
	hasLanguage := false
	if request.LanguageProfileId != nil {
		languageProfile, found := snapshot.LanguageProfile(*request.LanguageProfileId)
		if !found {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
				"Not found", "language_profile_id does not exist")
			return
		}
		languagePolicy = score.LanguagePolicy{
			AudioRequired: languageProfile.AudioRequired, AudioPreferred: languageProfile.AudioPreferred,
			SubtitleRequired: languageProfile.SubtitleRequired, SubtitlePreferred: languageProfile.SubtitlePreferred,
			PreferOriginalAudio: languageProfile.PreferOriginalAudio, RejectDubbed: languageProfile.RejectDubbed,
		}
		hasLanguage = true
	}

	var preferred []string
	if request.PreferredReleaseGroups != nil {
		preferred = *request.PreferredReleaseGroups
	}

	parsed := parse.Parse(request.Title)
	facts := factsFromParsed(parsed)

	var decision gen.Decision
	if hasLanguage {
		decision = decisionView(score.EvaluateWithLanguage(rules, facts, preferred, languagePolicy,
			facts.File.AudioLanguages, facts.File.SubtitleLanguages))
	} else {
		decision = decisionView(score.Evaluate(rules, facts, preferred))
	}

	writeJSON(w, s.logger, http.StatusOK, gen.ScoreSimulation{
		Parsed:   parsedTitleView(parsed),
		Decision: decision,
	})
}

// factsFromParsed stands the parser's claims in for measured facts (ADR-39): there
// is no real file to probe when simulating from a pasted title alone.
func factsFromParsed(parsed parse.Result) score.Facts {
	facts := score.Facts{
		Release: score.ReleaseFacts{ReleaseGroup: parsed.Attributes.ReleaseGroup},
		File: score.FileFacts{
			AudioCodec:   parsed.Attributes.Format,
			Lossless:     parsed.Attributes.Lossless,
			Source:       parsed.Attributes.Source,
			ReleaseGroup: parsed.Attributes.ReleaseGroup,
		},
	}
	if parsed.Attributes.BitrateKbps > 0 {
		facts.File.OverallBitrate = int64(parsed.Attributes.BitrateKbps) * 1000
	}
	if parsed.Attributes.Edition != "" {
		facts.File.EditionTokens = []string{parsed.Attributes.Edition}
	}
	return facts
}

func parsedTitleView(parsed parse.Result) gen.ParsedTitle {
	view := gen.ParsedTitle{
		Artist:       strPtr(parsed.Identity.Artist),
		Album:        strPtr(parsed.Identity.Album),
		Format:       strPtr(parsed.Attributes.Format),
		Source:       strPtr(parsed.Attributes.Source),
		Edition:      strPtr(parsed.Attributes.Edition),
		ReleaseGroup: strPtr(parsed.Attributes.ReleaseGroup),
		Confidence:   float32(parsed.Confidence),
		Unparsed:     &parsed.Unparsed,
	}
	if parsed.Identity.Year != 0 {
		view.Year = &parsed.Identity.Year
	}
	if parsed.Attributes.BitrateKbps != 0 {
		view.BitrateKbps = &parsed.Attributes.BitrateKbps
	}
	return view
}

func decisionView(decision model.Decision) gen.Decision {
	view := gen.Decision{
		Outcome:    gen.DecisionOutcome(decision.Outcome),
		Gates:      make([]gen.GateResult, len(decision.Gates)),
		Quality:    make([]gen.TierValue, len(decision.Quality.Tiers)),
		Components: make([]gen.ScoreComponent, len(decision.Components)),
		Rejections: make([]gen.Rejection, len(decision.Rejections)),
		Deferrals:  make([]gen.Deferral, len(decision.Deferrals)),
		Score:      float32(decision.Score()),
	}
	for i, gate := range decision.Gates {
		view.Gates[i] = gen.GateResult{Gate: gate.Gate, Passed: gate.Passed, Wanted: gate.Wanted, Got: gate.Got}
	}
	for i, tier := range decision.Quality.Tiers {
		rank := tier.Rank
		view.Quality[i] = gen.TierValue{Dimension: tier.Dimension, Rank: rank, Label: tier.Label, Detail: tier.Detail}
	}
	for i, component := range decision.Components {
		view.Components[i] = gen.ScoreComponent{Rule: component.Rule, Label: component.Label, Delta: float32(component.Delta)}
	}
	for i, rejection := range decision.Rejections {
		view.Rejections[i] = gen.Rejection{Rule: rejection.Rule, Field: rejection.Field, Wanted: rejection.Wanted, Got: rejection.Got}
	}
	for i, deferral := range decision.Deferrals {
		view.Deferrals[i] = gen.Deferral{Reason: deferral.Reason}
	}
	return view
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
