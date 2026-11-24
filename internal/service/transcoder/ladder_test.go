package transcoder_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func sizeFailure() []transcoder.Reason {
	return []transcoder.Reason{{Code: transcoder.ReasonResultAboveRange, Stream: -1}}
}

func ladderSetup() settings.TranscodeSetup {
	setup := checkSetup()
	setup.Encode.RateMode, setup.Encode.Quality = settings.RateQuality, 28
	setup.Ladder = settings.TranscodeLadder{ExtraPasses: 2, QualityStep: 2, BitrateStepPct: 20}
	return setup
}

func TestNextPass_QualityRisesByTheStepForTwoPassesThenStops(t *testing.T) {
	setup := ladderSetup()
	first := transcoder.FirstPass(setup, transcoder.Override{})
	if first.Pass != 0 || first.Quality != 28 {
		t.Fatalf("first = %+v", first)
	}
	second, ok := transcoder.NextPass(setup, encodePlan(), first, sizeFailure())
	if !ok || second.Pass != 1 || second.Quality != 30 {
		t.Fatalf("second = %+v, %v", second, ok)
	}
	third, ok := transcoder.NextPass(setup, encodePlan(), second, sizeFailure())
	if !ok || third.Pass != 2 || third.Quality != 32 {
		t.Fatalf("third = %+v, %v", third, ok)
	}
	if _, ok := transcoder.NextPass(setup, encodePlan(), third, sizeFailure()); ok {
		t.Error("a pass beyond ExtraPasses")
	}
}

func TestNextPass_BitrateModeScalesVideoAndAudioByTheStepEachPass(t *testing.T) {
	setup := ladderSetup()
	setup.Encode.RateMode = settings.RateBitrate
	first := transcoder.FirstPass(setup, transcoder.Override{VideoBitrateKbps: 3000})
	second, ok := transcoder.NextPass(setup, encodePlan(), first, sizeFailure())
	third, ok2 := transcoder.NextPass(setup, encodePlan(), second, sizeFailure())
	if !ok || !ok2 || second.Scale != 0.8 || third.Scale < 0.639 || third.Scale > 0.641 || third.VideoBitrateKbps != 3000 || third.Quality != 28 {
		t.Errorf("second = %+v, third = %+v", second, third)
	}
}

func TestNextPass_AnAudioOnlyPlanStepsTheBitrateAndLosslessAudioHasNothingToStep(t *testing.T) {
	setup := ladderSetup()
	lossy := transcoder.Plan{EncodesAudio: true, Audio: []transcoder.StreamPlan{{Index: 0, Type: transcode.StreamAudio, Action: transcoder.ActionEncode, BitrateKbps: 128}}}
	next, ok := transcoder.NextPass(setup, lossy, transcoder.FirstPass(setup, transcoder.Override{}), sizeFailure())
	if !ok || next.Scale != 0.8 || next.Quality != 28 {
		t.Errorf("next = %+v, %v; want the audio bitrate stepped, not the quality", next, ok)
	}
	lossless := transcoder.Plan{EncodesAudio: true, Audio: []transcoder.StreamPlan{{Index: 0, Type: transcode.StreamAudio, Action: transcoder.ActionEncode}}}
	if _, ok := transcoder.NextPass(setup, lossless, transcoder.FirstPass(setup, transcoder.Override{}), sizeFailure()); ok {
		t.Error("lossless audio has no bitrate to lower")
	}
}

func TestNextPass_OnlySizeFailuresOfAnEncodingPlanLadder(t *testing.T) {
	setup := ladderSetup()
	first := transcoder.FirstPass(setup, transcoder.Override{})
	for name, failures := range map[string][]transcoder.Reason{
		"duration":      {{Code: transcoder.ReasonDuration}},
		"stream count":  {{Code: transcoder.ReasonStreamCount}},
		"mixed":         {{Code: transcoder.ReasonResultLarger}, {Code: transcoder.ReasonDuration}},
		"none":          nil,
		"unreadable":    {{Code: transcoder.ReasonUnreadable}},
		"audio missing": {{Code: transcoder.ReasonAudioMissing}},
	} {
		if _, ok := transcoder.NextPass(setup, encodePlan(), first, failures); ok {
			t.Errorf("%s laddered", name)
		}
	}
	for _, code := range []transcoder.ReasonCode{transcoder.ReasonResultLarger, transcoder.ReasonResultAboveRange, transcoder.ReasonResultAboveMax} {
		if _, ok := transcoder.NextPass(setup, encodePlan(), first, []transcoder.Reason{{Code: code}}); !ok {
			t.Errorf("%s did not ladder", code)
		}
	}
	if _, ok := transcoder.NextPass(setup, transcoder.Plan{Remux: true}, first, sizeFailure()); ok {
		t.Error("a remux has nothing to ladder")
	}
	setup.Ladder.QualityStep = 0
	if _, ok := transcoder.NextPass(setup, encodePlan(), first, sizeFailure()); ok {
		t.Error("a zero step changes nothing")
	}
}

func TestFirstPass_APersonsOverrideWinsOverTheSetup(t *testing.T) {
	setup := ladderSetup()
	got := transcoder.FirstPass(setup, transcoder.Override{Quality: 24, Preset: "slow", Engine: "software"})
	if got.Quality != 24 || got.Preset != "slow" || got.Engine != "software" {
		t.Errorf("first = %+v", got)
	}
}
