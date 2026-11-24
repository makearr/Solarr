package transcoder

import (
	"math"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// PassSettings is what one pass of the ladder changes: a quality or bitrate of its own, and the marker it writes.
type PassSettings struct {
	// Quality is in software CRF terms; 0 takes the setup's.
	Quality float64
	// BitrateKbps replaces the planned video bitrate when above 0.
	BitrateKbps int64
	Marker      string
}

// BuildInput is everything BuildSpec turns into ffmpeg-side values.
type BuildInput struct {
	File    transcode.File
	Plan    Plan
	Target  settings.QualityTarget
	Setup   settings.TranscodeSetup
	Pass    PassSettings
	Encoder transcode.EncoderSupport
	Device  string
	// FullGPU keeps frames on the GPU; the caller asks transcode.CanRunOnGPU first.
	FullGPU, Tolerant bool
	Input, Output     string
}

// maxQuality is the highest quality number an encoder of the codec takes.
func maxQuality(codec transcode.Codec) float64 {
	if codec == transcode.CodecAV1 || codec == transcode.CodecVP9 {
		return 63
	}
	return 51
}

// BuildSpec is the only place settings become ffmpeg-side values: encoder, quality in the encoder's scale, bitrate cap, filters, tags.
func BuildSpec(in BuildInput) transcode.ConvertSpec {
	plan, setup := in.Plan, in.Setup
	spec := transcode.ConvertSpec{
		Input: in.Input, Output: in.Output, Container: plan.Container, Engine: in.Encoder.Engine, Device: in.Device,
		FullGPU: in.FullGPU, Tolerant: in.Tolerant, Marker: in.Pass.Marker, Kind: transcode.Audio,
	}
	if len(plan.Video) > 0 {
		spec.Kind = transcode.Video
	}
	sources := map[int]transcode.Stream{}
	for _, stream := range in.File.Streams {
		sources[stream.Index] = stream
	}
	for _, stream := range plan.Video {
		spec.Video = append(spec.Video, videoOut(in, stream, sources[stream.Index]))
	}
	for _, stream := range plan.Audio {
		spec.Audio = append(spec.Audio, audioOut(setup.Audio, stream, sources[stream.Index]))
	}
	for _, stream := range plan.Subtitles {
		if stream.Action == ActionDrop {
			continue
		}
		spec.Subtitles = append(spec.Subtitles, transcode.SubtitleOut{
			Index: stream.Index, Copy: stream.Action == ActionCopy, Codec: stream.Codec,
			Language: stream.Language, Title: stream.Title, Default: stream.Default, Forced: stream.Forced,
		})
	}
	for _, stream := range plan.Other {
		switch {
		case stream.Action != ActionCopy:
		case stream.Type == transcode.StreamVideo:
			spec.Covers = append(spec.Covers, stream.Index)
		case stream.Type == transcode.StreamAttachment:
			spec.KeepFonts = append(spec.KeepFonts, stream.Index)
		}
	}
	spec.CustomTags = len(spec.Covers) == 0
	spec.Tags = keptTags(in.File.Tags, setup.Audio.Normalize && plan.EncodesAudio)
	return spec
}

// keptTags are the file's tags a conversion writes back; the old marker is replaced, not copied.
func keptTags(tags map[string]string, normalising bool) map[string]string {
	kept := map[string]string{}
	for key, value := range tags {
		if strings.EqualFold(key, transcode.MarkerKey) || !transcode.KeepTag(key, normalising) {
			continue
		}
		kept[key] = value
	}
	return kept
}

func videoOut(in BuildInput, stream StreamPlan, source transcode.Stream) transcode.VideoOut {
	if stream.Action == ActionCopy {
		return transcode.VideoOut{Index: stream.Index, Copy: true}
	}
	setup, encode := in.Setup, in.Setup.Encode
	out := transcode.VideoOut{
		Index: stream.Index, Encoder: in.Encoder.Encoder, Preset: string(encode.Preset),
		TenBit: stream.BitDepth > 8 && in.Encoder.TenBit, HDR: string(stream.HDR),
		Filters: videoFilters(setup.Video, source.Interlaced, in.Target.Deinterlace),
	}
	out.Rate = rateControl(in, stream, source)
	if stream.Width != source.Width || stream.Height != source.Height {
		out.Width, out.Height = stream.Width, stream.Height
	}
	if stream.FPS > 0 && math.Abs(stream.FPS-source.FrameRate) > 0.01 {
		out.FPS = stream.FPS
	}
	if stream.HDR == HDRKeep {
		out.ColorPrimaries, out.ColorSpace, out.ColorTransfer = "bt2020", "bt2020nc", "smpte2084"
		if source.HDR == "hlg" {
			out.ColorTransfer = "arib-std-b67"
		}
	}
	return out
}

// rateControl puts the setup's quality or bitrate in the encoder's own scale; a GPU engine adds its offset.
func rateControl(in BuildInput, stream StreamPlan, source transcode.Stream) transcode.RateControl {
	encode := in.Setup.Encode
	rate := transcode.RateControl{Lookahead: in.Encoder.Lookahead, QualityMode: in.Encoder.QualityMode}
	if encode.RateMode == settings.RateBitrate {
		rate.Mode = transcode.RateModeBitrate
		rate.BitrateKbps = stream.BitrateKbps
		if in.Pass.BitrateKbps > 0 {
			rate.BitrateKbps = in.Pass.BitrateKbps
		}
		if sourceKbps := source.BitrateBps / 1000; sourceKbps > 0 && sourceKbps < rate.BitrateKbps {
			rate.BitrateKbps = sourceKbps
		}
		return rate
	}
	rate.Mode = transcode.RateModeQuality
	rate.Quality = encode.Quality
	if in.Pass.Quality > 0 {
		rate.Quality = in.Pass.Quality
	}
	if in.Encoder.Engine != transcode.Software {
		rate.Quality += encode.HardwareQualityOffset
	}
	rate.Quality = math.Min(math.Max(rate.Quality, 1), maxQuality(in.Encoder.Codec))
	return rate
}

func videoFilters(filters settings.VideoFilters, interlaced, targetDeinterlace bool) []transcode.VideoFilter {
	var out []transcode.VideoFilter
	if interlaced && (filters.Deinterlace || targetDeinterlace) {
		out = append(out, transcode.VideoFilter{Kind: transcode.FilterDeinterlace})
	}
	add := func(kind transcode.VideoFilterKind, strength settings.FilterStrength) {
		if strength != settings.StrengthOff && strength != "" {
			out = append(out, transcode.VideoFilter{Kind: kind, Strength: string(strength)})
		}
	}
	add(transcode.FilterDeflicker, filters.Deflicker)
	denoise := transcode.FilterBM3D
	switch filters.DenoiseMethod {
	case settings.DenoiseHQDN3D:
		denoise = transcode.FilterHQDN3D
	case settings.DenoiseNLMeans:
		denoise = transcode.FilterNLMeans
	}
	add(denoise, filters.Denoise)
	add(transcode.FilterDeblock, filters.Deblock)
	add(transcode.FilterDeband, filters.Deband)
	sharpen := transcode.FilterCAS
	if filters.SharpenMethod == settings.SharpenSmartBlur {
		sharpen = transcode.FilterSmartBlur
	}
	add(sharpen, filters.Sharpen)
	return out
}

func audioOut(filters settings.AudioFilters, stream StreamPlan, source transcode.Stream) transcode.AudioOut {
	out := transcode.AudioOut{Index: stream.Index, Language: stream.Language, Title: stream.Title, Default: stream.Default}
	if stream.Action == ActionCopy {
		out.Copy = true
		return out
	}
	out.Encoder, out.BitrateKbps, out.Channels, out.SampleRate = transcode.AudioEncoder(stream.Codec), stream.BitrateKbps, stream.Channels, stream.SampleRate
	add := func(kind transcode.AudioFilterKind, strength string) {
		out.Filters = append(out.Filters, transcode.AudioFilter{Kind: kind, Strength: strength})
	}
	if filters.Declip {
		add(transcode.AudioDeclip, "")
	}
	if filters.Denoise != settings.StrengthOff && filters.Denoise != "" {
		kind := transcode.AudioDenoiseFFT
		if filters.DenoiseMethod == settings.AudioDenoiseNLM {
			kind = transcode.AudioDenoiseNLM
		}
		add(kind, string(filters.Denoise))
	}
	if filters.DialogEnhance && source.Channels == 2 {
		add(transcode.AudioDialogEnhance, "")
	}
	if filters.Compress {
		add(transcode.AudioCompress, "")
	}
	if filters.Normalize {
		add(transcode.AudioNormalize, "")
	}
	return out
}
