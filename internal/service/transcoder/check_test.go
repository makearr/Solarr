package transcoder_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func checkSetup() settings.TranscodeSetup {
	setup := settings.DefaultTranscodeSetup(movieTarget())
	setup.Check = settings.TranscodeCheck{Duration: true, DurationSeconds: 2, Smaller: true, WithinRange: true, RangePct: 20, BelowMax: true, MaxPct: 150}
	return setup
}

func encodePlan() transcoder.Plan {
	return transcoder.Plan{Verdict: transcoder.Needed, EncodesVideo: true, EncodesAudio: true,
		Video: []transcoder.StreamPlan{{Index: 0, Type: transcode.StreamVideo, Action: transcoder.ActionEncode, BitrateKbps: 2000}},
		Audio: []transcoder.StreamPlan{{Index: 1, Type: transcode.StreamAudio, Action: transcoder.ActionEncode, BitrateKbps: 128}}}
}

func pair(target settings.QualityTarget, resultRatio float64) (original, result transcode.File) {
	streams := []transcode.Stream{video("h264", 1920, 1080, 30, 5000), audio(1, "aac", "en", 2, 48000, 128)}
	original = sized(transcode.File{Container: "mkv", Streams: streams}, target, 3)
	result = sized(transcode.File{Container: "mp4", Streams: streams}, target, resultRatio)
	return original, result
}

func failureCodes(outcome transcoder.Outcome) []transcoder.ReasonCode {
	var codes []transcoder.ReasonCode
	for _, failure := range outcome.Failures {
		codes = append(codes, failure.Code)
	}
	return codes
}

func TestCheck_AResultOnTargetAndSmallerIsAccepted(t *testing.T) {
	target := movieTarget()
	original, result := pair(target, 1.0)
	outcome := transcoder.Check(original, result, target, checkSetup(), encodePlan())
	if !outcome.Accepted || outcome.RatioPct != 100 || len(outcome.Warnings) != 0 {
		t.Errorf("outcome = %+v, want accepted at 100 %%", outcome)
	}
}

func TestCheck_EachSwitchableCheckFailsOnItsOwnAndSwitchesOff(t *testing.T) {
	target := movieTarget()
	cases := []struct {
		name   string
		mutate func(original, result *transcode.File)
		off    func(*settings.TranscodeCheck)
		want   transcoder.ReasonCode
	}{
		{"duration", func(_, r *transcode.File) { r.Duration += 3 * time.Second }, func(c *settings.TranscodeCheck) { c.Duration = false }, transcoder.ReasonDuration},
		{"smaller", func(o, r *transcode.File) { r.SizeBytes = o.SizeBytes }, func(c *settings.TranscodeCheck) { c.Smaller = false }, transcoder.ReasonResultLarger},
		{"above range", func(o, r *transcode.File) { o.SizeBytes *= 2; r.SizeBytes = int64(float64(r.SizeBytes) * 1.3) }, func(c *settings.TranscodeCheck) { c.WithinRange = false }, transcoder.ReasonResultAboveRange},
		{"above max", func(o, r *transcode.File) { o.SizeBytes *= 3; r.SizeBytes = int64(float64(r.SizeBytes) * 1.6) }, func(c *settings.TranscodeCheck) { c.BelowMax = false }, transcoder.ReasonResultAboveMax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			original, result := pair(target, 1.0)
			c.mutate(&original, &result)
			outcome := transcoder.Check(original, result, target, checkSetup(), encodePlan())
			if outcome.Accepted || !containsCode(failureCodes(outcome), c.want) {
				t.Fatalf("failures = %v, want %s", failureCodes(outcome), c.want)
			}
			setup := checkSetup()
			c.off(&setup.Check)
			if again := transcoder.Check(original, result, target, setup, encodePlan()); containsCode(failureCodes(again), c.want) {
				t.Errorf("%s still fails with its check off: %v", c.name, failureCodes(again))
			}
		})
	}
}

func containsCode(codes []transcoder.ReasonCode, want transcoder.ReasonCode) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

func TestCheck_ARemuxMayBeHalfAPercentLargerNotAWholeOne(t *testing.T) {
	target := movieTarget()
	original, result := pair(target, 1.0)
	plan := transcoder.Plan{Verdict: transcoder.Needed, Remux: true, Video: encodePlan().Video, Audio: encodePlan().Audio}
	setup := checkSetup()
	setup.Check.WithinRange, setup.Check.BelowMax = false, false
	result.SizeBytes = original.SizeBytes + original.SizeBytes*3/1000
	if outcome := transcoder.Check(original, result, target, setup, plan); !outcome.Accepted {
		t.Errorf("0.3 %% larger refused: %v", failureCodes(outcome))
	}
	result.SizeBytes = original.SizeBytes + original.SizeBytes/100
	if outcome := transcoder.Check(original, result, target, setup, plan); outcome.Accepted {
		t.Error("1 % larger accepted")
	}
}

func TestCheck_AlwaysChecksStreamCountsAudioAndReadability(t *testing.T) {
	target := movieTarget()
	original, result := pair(target, 1.0)
	result.Streams = result.Streams[:1]
	outcome := transcoder.Check(original, result, target, checkSetup(), encodePlan())
	want := []transcoder.Reason{
		{Code: transcoder.ReasonStreamCount, Stream: -1, Want: "audio:1", Have: "audio:0"},
		{Code: transcoder.ReasonAudioMissing, Stream: -1},
	}
	if !reflect.DeepEqual(outcome.Failures, want) {
		t.Errorf("failures = %+v, want %+v", outcome.Failures, want)
	}
	result.Streams = nil
	outcome = transcoder.Check(original, result, target, checkSetup(), encodePlan())
	if outcome.Accepted || !reflect.DeepEqual(failureCodes(outcome), []transcoder.ReasonCode{transcoder.ReasonUnreadable}) {
		t.Errorf("unreadable result: %+v", outcome)
	}
	setup := checkSetup()
	setup.Check = settings.TranscodeCheck{}
	if outcome := transcoder.Check(original, result, target, setup, encodePlan()); outcome.Accepted {
		t.Error("with every switchable check off an unreadable result was accepted")
	}
}

func TestCheck_BelowTheRangeIsAWarningAndATargetWithoutABitrateWarnsRangeUnknown(t *testing.T) {
	target := movieTarget()
	original, result := pair(target, 0.5)
	outcome := transcoder.Check(original, result, target, checkSetup(), encodePlan())
	if !outcome.Accepted || !reflect.DeepEqual(outcome.Warnings, []transcoder.Reason{{Code: transcoder.ReasonBelowRange, Stream: -1, Have: "50"}}) {
		t.Errorf("outcome = %+v, want accepted with below_range", outcome)
	}
	target.VideoBitrateKbps = nil
	outcome = transcoder.Check(original, result, target, checkSetup(), encodePlan())
	if !outcome.Accepted || outcome.RatioPct != 0 || !reflect.DeepEqual(outcome.Warnings, []transcoder.Reason{{Code: transcoder.ReasonRangeUnknown, Stream: -1}}) {
		t.Errorf("outcome = %+v, want accepted with range_unknown", outcome)
	}
}
