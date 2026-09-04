package score

import (
	"fmt"
	"strings"

	"github.com/makearr/MediaNexus/internal/model"
)

// gateStatus separates "actively violates the requirement" from "not measured
// yet" -- a pending probe is not a proven violation, so the latter becomes DEFER.
type gateStatus int

const (
	gatePassed gateStatus = iota
	gateFailed
	gateMissing
)

type gateOutcome struct {
	result model.GateResult
	status gateStatus
}

// unmeasuredFact marks a GateResult.Got produced when a field hasn't been probed
// -- EvaluateWithLanguage reads it back to pick DEFER over REJECT.
const unmeasuredFact = "unmeasured"

// evaluateGates runs every configured requirement -- never stops at the first
// failure, so a rejected candidate's UI can show everything wrong at once (ADR-40).
func evaluateGates(require Requirements, tiers map[string][][]string, target Target, hasTarget bool, facts Facts) (gates []model.GateResult, rejections []model.Rejection, deferrals []model.Deferral, outcome model.Outcome) {
	var outcomes []gateOutcome

	if require.Resolution != nil {
		outcomes = append(outcomes, resolutionGate(*require.Resolution, tiers["resolution"], facts.File.Resolution))
	}
	if require.AudioLanguage != nil {
		outcomes = append(outcomes, requireAnyOfGate("audio_language", require.AudioLanguage.AnyOf, facts.File.AudioLanguages))
	}
	if require.Bitrate == "reject_below_tier_min" {
		outcomes = append(outcomes, bitrateMinimumGate(target, hasTarget, facts.File.OverallBitrate))
	}
	if len(require.RejectCodecs) > 0 {
		outcomes = append(outcomes, rejectCodecsGate(require.RejectCodecs, facts.File.VideoCodec, facts.File.AudioCodec))
	}

	worst := gatePassed
	for _, one := range outcomes {
		gates = append(gates, one.result)
		switch one.status {
		case gateFailed:
			rejections = append(rejections, model.Rejection{Rule: one.result.Gate, Field: one.result.Gate, Wanted: one.result.Wanted, Got: one.result.Got})
			worst = gateFailed
		case gateMissing:
			deferrals = append(deferrals, model.Deferral{Reason: "unmeasured:" + one.result.Gate})
			if worst != gateFailed {
				worst = gateMissing
			}
		}
	}

	switch worst {
	case gateFailed:
		outcome = model.OutcomeReject
	case gateMissing:
		outcome = model.OutcomeDefer
	default:
		outcome = model.OutcomeAccept
	}
	return gates, rejections, deferrals, outcome
}

func resolutionGate(want ResolutionRange, groups [][]string, actual string) gateOutcome {
	wanted := fmt.Sprintf("%s..%s", want.Max, want.Min)
	if actual == "" {
		return gateOutcome{model.GateResult{Gate: "resolution", Passed: false, Wanted: wanted, Got: unmeasuredFact}, gateMissing}
	}
	if groups == nil {
		// No tiers.resolution to resolve the bound against -- nothing to gate on.
		return gateOutcome{model.GateResult{Gate: "resolution", Passed: true, Wanted: wanted, Got: actual}, gatePassed}
	}
	actualRank, actualFound := rankInGroups(groups, actual)
	minRank, minFound := rankInGroups(groups, want.Min)
	maxRank, maxFound := rankInGroups(groups, want.Max)
	if !actualFound || !minFound || !maxFound {
		return gateOutcome{model.GateResult{Gate: "resolution", Passed: false, Wanted: wanted, Got: actual}, gateMissing}
	}
	passed := actualRank >= maxRank && actualRank <= minRank
	result := model.GateResult{Gate: "resolution", Passed: passed, Wanted: wanted, Got: actual}
	if passed {
		return gateOutcome{result, gatePassed}
	}
	return gateOutcome{result, gateFailed}
}

func requireAnyOfGate(name string, want []string, present []string) gateOutcome {
	wanted := "any of [" + strings.Join(want, " ") + "]"
	if len(present) == 0 {
		return gateOutcome{model.GateResult{Gate: name, Passed: false, Wanted: wanted, Got: unmeasuredFact}, gateMissing}
	}
	got := strings.Join(present, " ")
	for _, language := range want {
		if containsFold(present, language) {
			return gateOutcome{model.GateResult{Gate: name, Passed: true, Wanted: wanted, Got: got}, gatePassed}
		}
	}
	return gateOutcome{model.GateResult{Gate: name, Passed: false, Wanted: wanted, Got: got}, gateFailed}
}

func bitrateMinimumGate(target Target, hasTarget bool, overallBitsPerSecond int64) gateOutcome {
	if !hasTarget {
		return gateOutcome{model.GateResult{Gate: "bitrate_min", Passed: true, Wanted: "n/a", Got: "no matching target"}, gatePassed}
	}
	if overallBitsPerSecond <= 0 {
		return gateOutcome{model.GateResult{Gate: "bitrate_min", Passed: false, Wanted: target.Min, Got: unmeasuredFact}, gateMissing}
	}
	minRate, err := parseBitRate(target.Min)
	if err != nil {
		return gateOutcome{model.GateResult{Gate: "bitrate_min", Passed: true, Wanted: target.Min, Got: "invalid target"}, gatePassed}
	}
	passed := float64(overallBitsPerSecond) >= minRate
	result := model.GateResult{Gate: "bitrate_min", Passed: passed, Wanted: target.Min, Got: fmt.Sprintf("%d bps", overallBitsPerSecond)}
	if passed {
		return gateOutcome{result, gatePassed}
	}
	return gateOutcome{result, gateFailed}
}

// rejectCodecsGate checks both video and audio codec fields, since "codec" means
// different things per media type, and this stays the one interpreter for both.
func rejectCodecsGate(rejected []string, videoCodec, audioCodec string) gateOutcome {
	wanted := "not [" + strings.Join(rejected, " ") + "]"
	if videoCodec == "" && audioCodec == "" {
		return gateOutcome{model.GateResult{Gate: "reject_codecs", Passed: false, Wanted: wanted, Got: unmeasuredFact}, gateMissing}
	}
	for _, codec := range rejected {
		if strings.EqualFold(codec, videoCodec) || strings.EqualFold(codec, audioCodec) {
			got := videoCodec
			if strings.EqualFold(codec, audioCodec) {
				got = audioCodec
			}
			return gateOutcome{model.GateResult{Gate: "reject_codecs", Passed: false, Wanted: wanted, Got: got}, gateFailed}
		}
	}
	got := videoCodec
	if got == "" {
		got = audioCodec
	}
	return gateOutcome{model.GateResult{Gate: "reject_codecs", Passed: true, Wanted: wanted, Got: got}, gatePassed}
}
