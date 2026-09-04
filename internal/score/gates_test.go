package score

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/model"
)

var seriesTiers = map[string][][]string{
	"resolution": {{"2160p"}, {"1080p", "1080i"}, {"720p"}, {"576p", "480p"}},
}

func seriesRequire() Requirements {
	anyOf := &AnyOfClause{AnyOf: []string{"en", "de"}}
	return Requirements{
		Resolution:    &ResolutionRange{Min: "720p", Max: "2160p"},
		AudioLanguage: anyOf,
		Bitrate:       "reject_below_tier_min",
		RejectCodecs:  []string{"xvid", "divx"},
	}
}

// TestEvaluateGates_EvaluatesEveryGateNotJustTheFirstFailure guards ADR-40's
// "hard gates evaluate ALL of them" rule (mutation-tested by hand: len(gates)==1).
func TestEvaluateGates_EvaluatesEveryGateNotJustTheFirstFailure(t *testing.T) {
	facts := Facts{File: FileFacts{
		Resolution:     "480p",         // fails the range
		VideoCodec:     "xvid",         // fails reject_codecs
		AudioLanguages: []string{"ru"}, // fails any_of
		OverallBitrate: 100,            // fails bitrate_min once a target is picked -- none matches here, so this one passes
	}}
	gates, rejections, _, outcome := evaluateGates(seriesRequire(), seriesTiers, Target{}, false, facts)

	if len(gates) != 4 {
		t.Fatalf("len(gates) = %d, want 4 (every configured requirement evaluated)", len(gates))
	}
	if len(rejections) != 3 {
		t.Fatalf("len(rejections) = %d, want 3 (resolution, audio_language, reject_codecs)", len(rejections))
	}
	if outcome != model.OutcomeReject {
		t.Errorf("outcome = %v, want REJECT", outcome)
	}
}

func TestEvaluateGates_AllPassAccepts(t *testing.T) {
	facts := Facts{File: FileFacts{
		Resolution:     "1080p",
		VideoCodec:     "h264",
		AudioLanguages: []string{"en"},
		OverallBitrate: 8_000_000,
	}}
	target := Target{Min: "6Mbps"}
	gates, rejections, deferrals, outcome := evaluateGates(seriesRequire(), seriesTiers, target, true, facts)

	if len(gates) != 4 {
		t.Fatalf("len(gates) = %d, want 4", len(gates))
	}
	for _, gate := range gates {
		if !gate.Passed {
			t.Errorf("gate %s: Passed = false, want true (%+v)", gate.Gate, gate)
		}
	}
	if len(rejections) != 0 || len(deferrals) != 0 {
		t.Errorf("rejections=%v deferrals=%v, want both empty", rejections, deferrals)
	}
	if outcome != model.OutcomeAccept {
		t.Errorf("outcome = %v, want ACCEPT", outcome)
	}
}

// TestEvaluateGates_UnmeasuredFieldDefersRatherThanRejects is the documented
// judgment call: a field not yet probed produces DEFER, not REJECT.
func TestEvaluateGates_UnmeasuredFieldDefersRatherThanRejects(t *testing.T) {
	facts := Facts{File: FileFacts{
		Resolution:     "", // not yet probed
		VideoCodec:     "h264",
		AudioLanguages: []string{"en"},
		OverallBitrate: 8_000_000,
	}}
	target := Target{Min: "6Mbps"}
	gates, rejections, deferrals, outcome := evaluateGates(seriesRequire(), seriesTiers, target, true, facts)

	if len(rejections) != 0 {
		t.Errorf("rejections = %v, want none (nothing actively failed)", rejections)
	}
	if len(deferrals) != 1 {
		t.Fatalf("len(deferrals) = %d, want 1", len(deferrals))
	}
	if outcome != model.OutcomeDefer {
		t.Errorf("outcome = %v, want DEFER", outcome)
	}
	found := false
	for _, gate := range gates {
		if gate.Gate == "resolution" {
			found = true
			if gate.Got != unmeasuredFact {
				t.Errorf("resolution gate Got = %q, want %q", gate.Got, unmeasuredFact)
			}
		}
	}
	if !found {
		t.Fatal("resolution gate not present in gates")
	}
}

func TestEvaluateGates_FailedBeatsMissingInOutcome(t *testing.T) {
	facts := Facts{File: FileFacts{
		Resolution:     "",     // missing
		VideoCodec:     "xvid", // actively fails
		AudioLanguages: []string{"en"},
		OverallBitrate: 8_000_000,
	}}
	target := Target{Min: "6Mbps"}
	_, rejections, deferrals, outcome := evaluateGates(seriesRequire(), seriesTiers, target, true, facts)

	if outcome != model.OutcomeReject {
		t.Errorf("outcome = %v, want REJECT (an active failure outranks a missing field)", outcome)
	}
	if len(rejections) != 1 || len(deferrals) != 1 {
		t.Errorf("rejections=%d deferrals=%d, want 1 and 1", len(rejections), len(deferrals))
	}
}

func TestResolutionGate_OutOfRangeIsRejected(t *testing.T) {
	groups := [][]string{{"2160p"}, {"1080p", "1080i"}, {"720p"}, {"576p", "480p"}}
	outcome := resolutionGate(ResolutionRange{Min: "720p", Max: "2160p"}, groups, "480p")
	if outcome.status != gateFailed {
		t.Errorf("status = %v, want gateFailed", outcome.status)
	}
}

func TestResolutionGate_MaxCapsResolutionToo(t *testing.T) {
	groups := [][]string{{"2160p"}, {"1080p", "1080i"}, {"720p"}, {"576p", "480p"}}
	outcome := resolutionGate(ResolutionRange{Min: "1080p", Max: "1080p"}, groups, "2160p")
	if outcome.status != gateFailed {
		t.Errorf("2160p against a max of 1080p: status = %v, want gateFailed", outcome.status)
	}
}

func TestRejectCodecsGate_CaseInsensitive(t *testing.T) {
	outcome := rejectCodecsGate([]string{"xvid", "divx"}, "XVID", "")
	if outcome.status != gateFailed {
		t.Errorf("status = %v, want gateFailed for a case-different match", outcome.status)
	}
}
