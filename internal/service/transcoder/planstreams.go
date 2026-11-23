package transcoder

import (
	"slices"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// containerHolds says which codecs a container takes: video, audio and subtitle codec names; an empty list takes any.
var containerHolds = map[string]struct{ video, audio, subtitle []string }{
	"mp4":  {video: []string{"h264", "h265", "av1", "vp9", "mpeg4"}, audio: []string{"aac", "ac3", "eac3", "mp3", "opus", "flac", "alac"}, subtitle: []string{"mov_text"}},
	"webm": {video: []string{"vp9", "av1", "vp8"}, audio: []string{"opus", "vorbis"}, subtitle: []string{"webvtt"}},
}

// coverContainers can carry a cover picture.
var coverContainers = map[string]bool{"mp4": true, "matroska": true, "flac": true, "mp3": true}

func holds(container, kind, codec string) bool {
	entry, restricted := containerHolds[score.CanonicalContainer(container)]
	if !restricted {
		return true
	}
	list := map[string][]string{"video": entry.video, "audio": entry.audio, "subtitle": entry.subtitle}[kind]
	return slices.Contains(list, codec)
}

// audioOnlyContainers hold no picture or subtitles of their own.
var audioOnlyContainers = map[string]bool{"flac": true, "mp3": true, "ogg": true, "opus": true, "wav": true}

func isAudioContainer(container string) bool {
	canonical := score.CanonicalContainer(container)
	return audioOnlyContainers[canonical] || strings.EqualFold(container, "m4a")
}

func (p *Plan) add(group *[]StreamPlan, stream StreamPlan) {
	if stream.Action == ActionDrop {
		p.Dropped = append(p.Dropped, stream)
		return
	}
	*group = append(*group, stream)
}

// planStreams fills the stream groups of the plan; a non-nil result blocks the file.
func planStreams(plan *Plan, file transcode.File, target settings.QualityTarget, setup settings.TranscodeSetup, wanted Languages) *Reason {
	audioOnly := target.TranscodeKind() == "audio"
	if blocked := planVideo(plan, file, target, setup, audioOnly); blocked != nil {
		return blocked
	}
	planAudio(plan, file, target, setup, wanted)
	planSubtitles(plan, file, setup, wanted, audioOnly)
	planOther(plan, file, audioOnly)
	return nil
}

func pixels(stream transcode.Stream) int { return stream.Width * stream.Height }

func planVideo(plan *Plan, file transcode.File, target settings.QualityTarget, setup settings.TranscodeSetup, audioOnly bool) *Reason {
	streams := realVideo(file)
	if len(streams) == 0 {
		return nil
	}
	main := slices.MaxFunc(streams, func(a, b transcode.Stream) int {
		if pixels(a) != pixels(b) {
			return pixels(a) - pixels(b)
		}
		if a.BitrateBps != b.BitrateBps {
			return int(a.BitrateBps - b.BitrateBps)
		}
		return b.Index - a.Index
	})
	for _, stream := range streams {
		if audioOnly || stream.Index != main.Index {
			plan.add(&plan.Video, StreamPlan{Index: stream.Index, Type: stream.Type, Action: ActionDrop, Reason: ReasonExtraVideoStream, Codec: stream.Codec})
		}
	}
	if audioOnly {
		return nil
	}
	return planMainVideo(plan, main, target, setup)
}

func planMainVideo(plan *Plan, stream transcode.Stream, target settings.QualityTarget, setup settings.TranscodeSetup) *Reason {
	streams := setup.Streams
	limit := fpsCap(target)
	haveRung, wantRung := rungOf(resolutionLabel(stream)), rungOf(target.Resolution)
	tonemap := lowerName(target.HDR) == "sdr" && stream.HDR != ""
	copyable := streams.CopyVideoOnTarget &&
		(target.VideoCodec == "" || videoCodecName(stream.Codec) == videoCodecName(target.VideoCodec)) &&
		holds(plan.Container, "video", videoCodecName(stream.Codec)) &&
		(wantRung < 0 || haveRung >= wantRung) &&
		(limit <= 0 || stream.FrameRate <= limit*frameRateSlack) &&
		withinPercent(stream.BitrateBps/1000, target.VideoBitrateKbps, streams.VideoCopyMaxPct) &&
		!tonemap && (!stream.Interlaced || (!target.Deinterlace && !setup.Video.Deinterlace))
	out := StreamPlan{Index: stream.Index, Type: stream.Type, Codec: stream.Codec, Width: stream.Width, Height: stream.Height, FPS: stream.FrameRate, BitDepth: stream.BitDepth, HDR: hdrOf(stream, tonemap)}
	if copyable {
		out.Action = ActionCopy
		plan.add(&plan.Video, out)
		return nil
	}
	if stream.DolbyVisionProfile == 5 {
		return &Reason{Code: ReasonDolbyVisionProfile5, Stream: stream.Index}
	}
	out.Action, plan.EncodesVideo = ActionEncode, true
	if target.VideoCodec != "" {
		out.Codec = target.VideoCodec
	}
	out.Width, out.Height = scaledSize(stream, wantRung, haveRung, target, streams)
	out.FPS = outputFPS(stream.FrameRate, limit, streams, target)
	out.BitDepth = outputBitDepth(stream.BitDepth, target)
	if setup.Encode.RateMode == settings.RateBitrate {
		out.BitrateKbps = setup.Encode.VideoBitrateKbps
		if out.BitrateKbps == 0 && target.VideoBitrateKbps != nil {
			out.BitrateKbps = *target.VideoBitrateKbps
		}
	}
	plan.add(&plan.Video, out)
	return nil
}

func hdrOf(stream transcode.Stream, tonemap bool) HDRAction {
	switch {
	case stream.HDR == "":
		return HDRNone
	case tonemap:
		return HDRTonemap
	}
	return HDRKeep
}

func resolutionLabel(stream transcode.Stream) string {
	return quality.ResolutionOf(stream.Width, stream.Height)
}

// withinPercent says a measured kbit/s is at most pct percent of the target's; unknown on either side passes.
func withinPercent(kbps int64, target *int64, pct int64) bool {
	return kbps <= 0 || target == nil || *target <= 0 || kbps*100 <= *target*pct
}

// scaledSize is the output size: the target's box when the source is larger (and downscaling is on) or smaller (and upscaling is off).
func scaledSize(stream transcode.Stream, wantRung, haveRung int, target settings.QualityTarget, streams settings.TranscodeStreams) (width, height int) {
	width, height = stream.Width, stream.Height
	boxWidth := targetWidth(target.Resolution)
	if boxWidth == 0 || wantRung < 0 || stream.Width <= 0 {
		return width, height
	}
	larger := haveRung < wantRung
	if (larger && streams.Downscale) || (!larger && haveRung > wantRung && !streams.NoUpscale) {
		width = boxWidth
		height = (stream.Height*boxWidth/stream.Width + 1) / 2 * 2
	}
	return width, height
}

func outputFPS(source, limit float64, streams settings.TranscodeStreams, target settings.QualityTarget) float64 {
	switch {
	case limit > 0 && source > limit*frameRateSlack && streams.ReduceFPS:
		return reducedFPS(source, limit)
	case target.FrameRate > 0 && source > 0 && source < target.FrameRate/frameRateSlack && !streams.NoFPSIncrease:
		return target.FrameRate
	}
	return source
}

// outputBitDepth keeps the source's depth up to what the target names; H.264 stays 8-bit.
func outputBitDepth(source int, target settings.QualityTarget) int {
	depth := max(source, 8)
	if target.BitDepth > 0 && int64(depth) > target.BitDepth {
		depth = int(target.BitDepth)
	}
	if videoCodecName(target.VideoCodec) == "h264" {
		return 8
	}
	return depth
}

// audioTargetKbps is the bitrate the target names for audio: its own on an audio target, else the audio part of a video one.
func audioTargetKbps(target settings.QualityTarget) *int64 {
	if target.IsVideo() {
		return target.AudioBitrateKbps
	}
	return target.BitrateKbps
}

// betterAudio orders tracks of one language, best first: not commentary, channels up to the target's, default, bitrate, lowest index.
func betterAudio(channelCap int) func(a, b transcode.Stream) int {
	return func(a, b transcode.Stream) int {
		if a.Commentary != b.Commentary {
			return boolRank(a.Commentary) - boolRank(b.Commentary)
		}
		if rank := channelRank(a.Channels, channelCap) - channelRank(b.Channels, channelCap); rank != 0 {
			return rank
		}
		if a.Default != b.Default {
			return boolRank(b.Default) - boolRank(a.Default)
		}
		if a.BitrateBps != b.BitrateBps {
			if a.BitrateBps > b.BitrateBps {
				return -1
			}
			return 1
		}
		return a.Index - b.Index
	}
}

func boolRank(value bool) int {
	if value {
		return 1
	}
	return 0
}

// channelRank sorts within the cap by most channels first, then above it by fewest: lower is better.
func channelRank(channels, channelCap int) int {
	if channelCap <= 0 {
		return -channels
	}
	if channels <= channelCap {
		return -channels
	}
	return 100 + channels
}

func planAudio(plan *Plan, file transcode.File, target settings.QualityTarget, setup settings.TranscodeSetup, wanted Languages) {
	var all []transcode.Stream
	for _, stream := range file.Streams {
		if stream.Type == transcode.StreamAudio {
			all = append(all, stream)
		}
	}
	if len(all) == 0 {
		return
	}
	channelCap := layoutChannels(target.AudioChannels)
	keep, drops := chooseAudio(all, wanted.Audio, channelCap, setup.Streams.RemoveUnwantedAudio)
	for _, stream := range drops {
		plan.add(&plan.Audio, StreamPlan{Index: stream.stream.Index, Type: transcode.StreamAudio, Action: ActionDrop, Reason: stream.reason, Codec: stream.stream.Codec, Language: stream.stream.Language})
	}
	for position, stream := range keep {
		one := planOneAudio(stream, target, setup)
		one.Default = position == 0
		if one.Action == ActionEncode {
			plan.EncodesAudio = true
		}
		plan.add(&plan.Audio, one)
	}
}

type droppedStream struct {
	stream transcode.Stream
	reason ReasonCode
}

// chooseAudio picks the tracks to keep in output order (wanted languages first, then the rest by index) and those to drop.
func chooseAudio(all []transcode.Stream, wantedLanguages []string, channelCap int, removeUnwanted bool) (keep []transcode.Stream, drops []droppedStream) {
	hasWanted := slices.ContainsFunc(all, func(stream transcode.Stream) bool { return slices.Contains(wantedLanguages, stream.Language) })
	if !hasWanted || !removeUnwanted {
		sorted := slices.Clone(all)
		order := func(stream transcode.Stream) int {
			if position := slices.Index(wantedLanguages, stream.Language); position >= 0 {
				return position
			}
			return len(wantedLanguages)
		}
		sort.SliceStable(sorted, func(i, j int) bool { return order(sorted[i]) < order(sorted[j]) })
		if hasWanted {
			return dedupeWanted(sorted, wantedLanguages, channelCap, removeUnwanted)
		}
		return sorted, nil
	}
	return dedupeWanted(all, wantedLanguages, channelCap, removeUnwanted)
}

// dedupeWanted keeps the best track of each wanted language; untagged tracks stay, unwanted-language ones go when removal is on.
func dedupeWanted(all []transcode.Stream, wantedLanguages []string, channelCap int, removeUnwanted bool) (keep []transcode.Stream, drops []droppedStream) {
	best := map[string]transcode.Stream{}
	for _, language := range wantedLanguages {
		var group []transcode.Stream
		for _, stream := range all {
			if stream.Language == language {
				group = append(group, stream)
			}
		}
		if len(group) > 0 {
			best[language] = slices.MinFunc(group, betterAudio(channelCap))
		}
	}
	for _, language := range wantedLanguages {
		if stream, found := best[language]; found && !slices.ContainsFunc(keep, func(kept transcode.Stream) bool { return kept.Index == stream.Index }) {
			keep = append(keep, stream)
		}
	}
	for _, stream := range all {
		switch {
		case slices.ContainsFunc(keep, func(kept transcode.Stream) bool { return kept.Index == stream.Index }):
		case stream.Language == "":
			keep = append(keep, stream)
		case !slices.Contains(wantedLanguages, stream.Language):
			if removeUnwanted {
				drops = append(drops, droppedStream{stream, ReasonUnwantedAudio})
			} else {
				keep = append(keep, stream)
			}
		case !removeUnwanted:
			keep = append(keep, stream)
		case stream.Commentary:
			drops = append(drops, droppedStream{stream, ReasonCommentary})
		default:
			drops = append(drops, droppedStream{stream, ReasonDuplicateLanguage})
		}
	}
	return keep, drops
}

func planOneAudio(stream transcode.Stream, target settings.QualityTarget, setup settings.TranscodeSetup) StreamPlan {
	streams := setup.Streams
	channelCap := layoutChannels(target.AudioChannels)
	kbps := stream.BitrateBps / 1000
	one := StreamPlan{
		Index: stream.Index, Type: transcode.StreamAudio, Codec: stream.Codec, Channels: stream.Channels, SampleRate: stream.SampleRate,
		BitrateKbps: kbps, Language: stream.Language, Title: stream.Title,
	}
	sameCodec := target.AudioCodec == "" || lowerName(target.AudioCodec) == lowerName(stream.Codec)
	rateOK := target.SampleRateHz == 0 || int64(stream.SampleRate) <= target.SampleRateHz
	channelsOK := channelCap == 0 || stream.Channels <= channelCap
	if streams.CopyAudioOnTarget && sameCodec && rateOK && channelsOK && withinPercent(kbps, audioTargetKbps(target), streams.AudioCopyMaxPct) {
		one.Action = ActionCopy
		return one
	}
	one.Action = ActionEncode
	if target.AudioCodec != "" {
		one.Codec = lowerName(target.AudioCodec)
	}
	switch {
	case channelCap > 0 && stream.Channels > channelCap && streams.Downmix, channelCap > 0 && stream.Channels < channelCap && !streams.NoUpmix:
		one.Channels = channelCap
	}
	switch {
	case target.SampleRateHz > 0 && int64(stream.SampleRate) > target.SampleRateHz && streams.Downmix,
		target.SampleRateHz > 0 && int64(stream.SampleRate) < target.SampleRateHz && !streams.NoSampleRateIncrease:
		one.SampleRate = int(target.SampleRateHz)
	}
	one.BitrateKbps = 0
	if want := audioTargetKbps(target); want != nil && !losslessCodec(one.Codec) {
		one.BitrateKbps = *want
		if kbps > 0 && kbps < *want && !losslessCodec(stream.Codec) {
			one.BitrateKbps = kbps
		}
	}
	return one
}

func losslessCodec(codec string) bool {
	return codec == "flac" || codec == "alac" || strings.HasPrefix(codec, "pcm_")
}

func planSubtitles(plan *Plan, file transcode.File, setup settings.TranscodeSetup, wanted Languages, audioOnly bool) {
	for _, stream := range file.Streams {
		if stream.Type != transcode.StreamSubtitle {
			continue
		}
		one := StreamPlan{Index: stream.Index, Type: stream.Type, Codec: stream.Codec, Language: stream.Language, Title: stream.Title, Default: stream.Default, Forced: stream.Forced}
		switch {
		case audioOnly || isAudioContainer(plan.Container):
			one.Action, one.Reason = ActionDrop, ReasonContainerCannot
		case setup.Streams.RemoveUnwantedSubtitles && unwanted(stream.Language, wanted.Subtitles):
			one.Action, one.Reason = ActionDrop, ReasonUnwantedSubtitle
		case stream.Picture && score.CanonicalContainer(plan.Container) != "matroska":
			one.Action, one.Reason = ActionDrop, ReasonPictureSubtitle
		case stream.Picture || holds(plan.Container, "subtitle", stream.Codec):
			one.Action = ActionCopy
		default:
			one.Action, one.Codec = ActionConvert, convertedSubtitle(plan.Container, stream.Codec)
		}
		plan.add(&plan.Subtitles, one)
	}
}

// convertedSubtitle is the text subtitle codec a container takes in place of codec.
func convertedSubtitle(container, codec string) string {
	switch score.CanonicalContainer(container) {
	case "mp4":
		return "mov_text"
	case "webm":
		return "webvtt"
	}
	if codec == "ass" || codec == "ssa" {
		return "ass"
	}
	return "subrip"
}

func planOther(plan *Plan, file transcode.File, audioOnly bool) {
	container := score.CanonicalContainer(plan.Container)
	fontsKept := container == "matroska" && slices.ContainsFunc(plan.Subtitles, func(stream StreamPlan) bool { return stream.Codec == "ass" || stream.Codec == "ssa" })
	for _, stream := range file.Streams {
		one := StreamPlan{Index: stream.Index, Type: stream.Type, Codec: stream.Codec}
		switch {
		case stream.Type == transcode.StreamVideo && stream.AttachedPicture:
			if coverContainers[container] || strings.EqualFold(plan.Container, "m4a") {
				one.Action = ActionCopy
			} else {
				one.Action, one.Reason = ActionDrop, ReasonContainerCannot
			}
		case stream.Type == transcode.StreamAttachment:
			if fontsKept && !audioOnly {
				one.Action = ActionCopy
			} else {
				one.Action, one.Reason = ActionDrop, ReasonAttachment
			}
		case stream.Type == transcode.StreamData:
			one.Action, one.Reason = ActionDrop, ReasonDataStream
		case stream.Type == transcode.StreamUnknown:
			one.Action, one.Reason = ActionDrop, ReasonUnknownStream
		default:
			continue
		}
		plan.add(&plan.Other, one)
	}
}
