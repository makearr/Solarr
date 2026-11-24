package transcoder

import (
	"math"
	"strconv"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// remuxSlack lets a result that re-encoded nothing be this much larger than its source (container overhead).
const remuxSlack = 1.005

// Outcome is whether a finished conversion may replace its original.
type Outcome struct {
	Accepted bool
	Failures []Reason
	Warnings []Reason
	// RatioPct is the result's overall bitrate as a percentage of what the target expects; 0 when unknown.
	RatioPct int
}

// Check judges a finished conversion against the plan and the setup's checks.
func Check(original, result transcode.File, target settings.QualityTarget, setup settings.TranscodeSetup, plan Plan) Outcome {
	var out Outcome
	if len(result.Streams) == 0 {
		out.Failures = []Reason{{Code: ReasonUnreadable, Stream: -1}}
		return out
	}
	out.Failures = append(out.Failures, streamCountFailures(result, plan)...)
	if hasStream(original, transcode.StreamAudio) && !hasStream(result, transcode.StreamAudio) {
		out.Failures = append(out.Failures, Reason{Code: ReasonAudioMissing, Stream: -1})
	}
	check := setup.Check
	if check.Duration {
		delta := math.Abs((result.Duration - original.Duration).Seconds())
		if delta > float64(check.DurationSeconds) {
			out.Failures = append(out.Failures, Reason{Code: ReasonDuration, Stream: -1, Want: strconv.FormatInt(check.DurationSeconds, 10), Have: strconv.FormatFloat(math.Round(delta*10)/10, 'f', -1, 64)})
		}
	}
	if check.Smaller && original.SizeBytes > 0 {
		larger := result.SizeBytes >= original.SizeBytes
		if !plan.EncodesVideo && !plan.EncodesAudio {
			larger = float64(result.SizeBytes) > float64(original.SizeBytes)*remuxSlack
		}
		if larger {
			out.Failures = append(out.Failures, Reason{Code: ReasonResultLarger, Stream: -1, Want: "100", Have: percent(float64(result.SizeBytes) / float64(original.SizeBytes))})
		}
	}
	ratio, known := sizeRatio(result, target)
	switch {
	case !known && (check.WithinRange || check.BelowMax):
		out.Warnings = append(out.Warnings, Reason{Code: ReasonRangeUnknown, Stream: -1})
	case known:
		out.RatioPct = int(math.Round(ratio * 100))
		if check.WithinRange && ratio > 1+float64(check.RangePct)/100 {
			out.Failures = append(out.Failures, Reason{Code: ReasonResultAboveRange, Stream: -1, Want: strconv.FormatInt(100+check.RangePct, 10), Have: percent(ratio)})
		}
		if check.BelowMax && ratio > float64(check.MaxPct)/100 {
			out.Failures = append(out.Failures, Reason{Code: ReasonResultAboveMax, Stream: -1, Want: strconv.FormatInt(check.MaxPct, 10), Have: percent(ratio)})
		}
		if check.WithinRange && ratio < 1-float64(check.RangePct)/100 {
			out.Warnings = append(out.Warnings, Reason{Code: ReasonBelowRange, Stream: -1, Have: percent(ratio)})
		}
	}
	out.Accepted = len(out.Failures) == 0
	return out
}

// streamCountFailures compares the result's streams, by type, with the plan's.
func streamCountFailures(result transcode.File, plan Plan) []Reason {
	var out []Reason
	for _, want := range []struct {
		name  string
		count int
		kind  transcode.StreamType
	}{
		{"video", len(plan.Video), transcode.StreamVideo}, {"audio", len(plan.Audio), transcode.StreamAudio}, {"subtitle", len(plan.Subtitles), transcode.StreamSubtitle},
	} {
		have := 0
		for _, stream := range result.Streams {
			if stream.Type == want.kind && !stream.AttachedPicture {
				have++
			}
		}
		if have != want.count {
			out = append(out, Reason{Code: ReasonStreamCount, Stream: -1, Want: want.name + ":" + strconv.Itoa(want.count), Have: want.name + ":" + strconv.Itoa(have)})
		}
	}
	return out
}
