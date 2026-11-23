package transcoder

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// MarkerTag is the file tag (lower-cased by the probe) that says Solarr converted the file.
const MarkerTag = "solarr_transcoded"

// frameRateSlack lets 29.97 pass for a 30 fps cap.
const frameRateSlack = 1.01

// Analyze decides what converting this file to the target would do, and whether a conversion is needed at all.
func Analyze(file transcode.File, target settings.QualityTarget, setup settings.TranscodeSetup, wanted Languages) Plan {
	if target.TranscodeKind() == "other" {
		return Plan{Verdict: Blocked, Reasons: []Reason{{Code: ReasonUnsupportedTarget, Stream: -1}}}
	}
	if setup.Skip.TranscodedBefore && file.Tags[MarkerTag] != "" {
		return Plan{Verdict: NotNeeded, Reasons: []Reason{{Code: ReasonTranscodedBefore, Stream: -1, Have: file.Tags[MarkerTag]}}}
	}
	reasons, warnings := skipRules(file, target, setup, wanted)
	if len(reasons) == 0 {
		return Plan{Verdict: NotNeeded, Warnings: warnings}
	}
	plan := Plan{Verdict: Needed, Reasons: reasons, Warnings: warnings, Container: outputContainer(file, target)}
	if len(realVideo(file)) == 0 && !hasStream(file, transcode.StreamAudio) {
		return Plan{Verdict: Blocked, Reasons: []Reason{{Code: ReasonNoStreams, Stream: -1}}}
	}
	if blocked := planStreams(&plan, file, target, setup, wanted); blocked != nil {
		return Plan{Verdict: Blocked, Reasons: []Reason{*blocked}}
	}
	plan.Remux = !plan.EncodesVideo && !plan.EncodesAudio
	if allCopied(plan) && len(plan.Dropped) == 0 && score.CanonicalContainer(file.Container) == score.CanonicalContainer(plan.Container) {
		return Plan{Verdict: NotNeeded, Reasons: []Reason{{Code: ReasonNothingToDo, Stream: -1}}, Warnings: warnings}
	}
	return plan
}

func hasStream(file transcode.File, kind transcode.StreamType) bool {
	return slices.ContainsFunc(file.Streams, func(stream transcode.Stream) bool { return stream.Type == kind })
}

func realVideo(file transcode.File) []transcode.Stream {
	var out []transcode.Stream
	for _, stream := range file.Streams {
		if stream.Type == transcode.StreamVideo && !stream.AttachedPicture {
			out = append(out, stream)
		}
	}
	return out
}

func allCopied(plan Plan) bool {
	for _, group := range [][]StreamPlan{plan.Video, plan.Audio, plan.Subtitles, plan.Other} {
		for _, stream := range group {
			if stream.Action != ActionCopy {
				return false
			}
		}
	}
	return true
}

// outputContainer is the target's first container, else the file's own.
func outputContainer(file transcode.File, target settings.QualityTarget) string {
	if listed := containerList(target); len(listed) > 0 {
		return listed[0]
	}
	return score.CanonicalContainer(file.Container)
}

func containerList(target settings.QualityTarget) []string {
	var out []string
	for _, name := range strings.Split(target.Container, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// videoCodecName is ffprobe's or a target's video codec name in one vocabulary.
func videoCodecName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hevc", "h265", "x265":
		return "h265"
	case "h264", "avc", "x264":
		return "h264"
	case "mpeg2video":
		return "mpeg2"
	}
	return strings.ToLower(strings.TrimSpace(name))
}

func codecIn(codec string, want string, also []string, canonical func(string) string) bool {
	if canonical(want) == canonical(codec) {
		return true
	}
	return slices.ContainsFunc(also, func(other string) bool { return canonical(other) == canonical(codec) })
}

func lowerName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// rungOf is a resolution label's place on the ladder, 0 the largest; -1 for a label off it.
func rungOf(label string) int { return slices.Index(quality.Resolutions, label) }

// targetWidth is the picture width of a resolution label.
func targetWidth(resolution string) int {
	return map[string]int{"2160p": 3840, "1440p": 2560, "1080p": 1920, "720p": 1280, "576p": 1024, "480p": 854}[resolution]
}

// fpsCap is the frame rate no stream should exceed, 0 when the target names none.
func fpsCap(target settings.QualityTarget) float64 {
	if target.MaxFrameRate > 0 {
		return target.MaxFrameRate
	}
	return target.FrameRate
}

// reducedFPS divides a rate by whole numbers until it fits the cap: 59.94 to 29.97, 50 to 25.
func reducedFPS(source, limit float64) float64 {
	if limit <= 0 || source <= limit*frameRateSlack {
		return source
	}
	return source / math.Ceil(source/limit-0.01)
}

// layoutChannels is the channel count of a layout such as "5.1"; 0 when it is not one.
func layoutChannels(layout string) int {
	whole, fraction, found := strings.Cut(layout, ".")
	main, err := strconv.Atoi(whole)
	if err != nil {
		return 0
	}
	if found {
		extra, _ := strconv.Atoi(fraction)
		main += extra
	}
	return main
}

func channelCount(layout string, channels int) int {
	if channels > 0 {
		return channels
	}
	return layoutChannels(layout)
}

func percent(ratio float64) string { return strconv.Itoa(int(math.Round(ratio * 100))) }

// skipRules lists every active rule the file fails, and the notes that do not make a file need converting.
func skipRules(file transcode.File, target settings.QualityTarget, setup settings.TranscodeSetup, wanted Languages) (reasons, warnings []Reason) {
	skip := setup.Skip
	if skip.ContainerMatches {
		reasons = append(reasons, containerRule(file, target)...)
	}
	if skip.CodecMatches {
		reasons = append(reasons, codecRule(file, target, skip)...)
	}
	if skip.ResolutionAtOrBelow {
		reasons = append(reasons, resolutionRule(file, target)...)
	}
	reasons = append(reasons, hdrRule(file, target)...)
	if skip.RateAtOrBelow {
		reasons = append(reasons, rateRule(file, target, setup.Streams)...)
	}
	if skip.OnlyWantedLanguages {
		reasons = append(reasons, languageRule(file, setup.Streams, wanted)...)
	}
	ratio, known := sizeRatio(file, target)
	switch {
	case !known && (skip.WithinRange || skip.BelowMax):
		warnings = append(warnings, Reason{Code: ReasonRangeUnknown, Stream: -1})
	case known:
		if skip.WithinRange && ratio > 1+float64(skip.RangePct)/100 {
			reasons = append(reasons, Reason{Code: ReasonAboveRange, Stream: -1, Want: strconv.FormatInt(100+skip.RangePct, 10), Have: percent(ratio)})
		}
		if skip.BelowMax && ratio > float64(skip.MaxPct)/100 {
			reasons = append(reasons, Reason{Code: ReasonAboveMax, Stream: -1, Want: strconv.FormatInt(skip.MaxPct, 10), Have: percent(ratio)})
		}
		if ratio < 1-float64(skip.RangePct)/100 {
			warnings = append(warnings, Reason{Code: ReasonBelowRange, Stream: -1, Have: percent(ratio)})
		}
	}
	return reasons, warnings
}

// sizeRatio is the file's overall bitrate over the target's expected one.
func sizeRatio(file transcode.File, target settings.QualityTarget) (float64, bool) {
	if file.Duration <= 0 || file.SizeBytes <= 0 {
		return 0, false
	}
	kbps := float64(file.SizeBytes) * 8 / file.Duration.Seconds() / 1000
	ratio, _, known := quality.SizeRatio(target, kbps)
	return ratio, known
}

func containerRule(file transcode.File, target settings.QualityTarget) []Reason {
	listed := containerList(target)
	have := score.CanonicalContainer(file.Container)
	if len(listed) == 0 || slices.ContainsFunc(listed, func(name string) bool { return score.CanonicalContainer(name) == have }) {
		return nil
	}
	return []Reason{{Code: ReasonContainer, Stream: -1, Want: listed[0], Have: have}}
}

func codecRule(file transcode.File, target settings.QualityTarget, skip settings.TranscodeSkip) []Reason {
	var out []Reason
	if target.VideoCodec != "" {
		for _, stream := range realVideo(file) {
			if !codecIn(stream.Codec, target.VideoCodec, skip.AlsoVideoCodecs, videoCodecName) {
				out = append(out, Reason{Code: ReasonVideoCodec, Stream: stream.Index, Want: target.VideoCodec, Have: stream.Codec})
			}
		}
	}
	if target.AudioCodec != "" {
		for _, stream := range file.Streams {
			if stream.Type == transcode.StreamAudio && !codecIn(stream.Codec, target.AudioCodec, skip.AlsoAudioCodecs, lowerName) {
				out = append(out, Reason{Code: ReasonAudioCodec, Stream: stream.Index, Want: target.AudioCodec, Have: stream.Codec})
			}
		}
	}
	return out
}

func resolutionRule(file transcode.File, target settings.QualityTarget) []Reason {
	want := rungOf(target.Resolution)
	if want < 0 {
		return nil
	}
	var out []Reason
	for _, stream := range realVideo(file) {
		have := quality.ResolutionOf(stream.Width, stream.Height)
		if rungOf(have) < want {
			out = append(out, Reason{Code: ReasonResolution, Stream: stream.Index, Want: target.Resolution, Have: have})
		}
	}
	return out
}

// hdrRule is not switchable: a target that wants SDR cannot keep an HDR picture.
func hdrRule(file transcode.File, target settings.QualityTarget) []Reason {
	if lowerName(target.HDR) != "sdr" {
		return nil
	}
	var out []Reason
	for _, stream := range realVideo(file) {
		if stream.HDR != "" {
			out = append(out, Reason{Code: ReasonHDR, Stream: stream.Index, Want: "sdr", Have: stream.HDR})
		}
	}
	return out
}

func rateRule(file transcode.File, target settings.QualityTarget, streams settings.TranscodeStreams) []Reason {
	var out []Reason
	limit := fpsCap(target)
	channelCap := layoutChannels(target.AudioChannels)
	for _, stream := range file.Streams {
		switch {
		case stream.Type == transcode.StreamVideo && !stream.AttachedPicture && limit > 0 && stream.FrameRate > limit*frameRateSlack:
			out = append(out, Reason{Code: ReasonFrameRate, Stream: stream.Index, Want: formatRate(limit), Have: formatRate(stream.FrameRate)})
		case stream.Type == transcode.StreamAudio && target.SampleRateHz > 0 && int64(stream.SampleRate) > target.SampleRateHz:
			out = append(out, Reason{Code: ReasonSampleRate, Stream: stream.Index, Want: strconv.FormatInt(target.SampleRateHz, 10), Have: strconv.Itoa(stream.SampleRate)})
		case stream.Type == transcode.StreamAudio && streams.Downmix && channelCap > 0 && stream.Channels > channelCap:
			out = append(out, Reason{Code: ReasonChannels, Stream: stream.Index, Want: strconv.Itoa(channelCap), Have: strconv.Itoa(stream.Channels)})
		}
	}
	return out
}

func formatRate(rate float64) string {
	return strconv.FormatFloat(math.Round(rate*100)/100, 'f', -1, 64)
}

func languageRule(file transcode.File, streams settings.TranscodeStreams, wanted Languages) []Reason {
	var out []Reason
	for _, stream := range file.Streams {
		switch {
		case stream.Type == transcode.StreamAudio && streams.RemoveUnwantedAudio && unwanted(stream.Language, wanted.Audio):
			out = append(out, Reason{Code: ReasonUnwantedAudio, Stream: stream.Index, Want: strings.Join(wanted.Audio, ","), Have: stream.Language})
		case stream.Type == transcode.StreamSubtitle && streams.RemoveUnwantedSubtitles && unwanted(stream.Language, wanted.Subtitles):
			out = append(out, Reason{Code: ReasonUnwantedSubtitle, Stream: stream.Index, Want: strings.Join(wanted.Subtitles, ","), Have: stream.Language})
		}
	}
	return out
}

// unwanted says a stream's language is stated and not among the wanted ones; untagged streams and an empty list never are.
func unwanted(language string, wanted []string) bool {
	return language != "" && len(wanted) > 0 && !slices.Contains(wanted, language)
}
