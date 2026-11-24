package transcoder_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func assertSpec(t *testing.T, got, want transcode.ConvertSpec) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spec =\n %+v\nwant\n %+v", got, want)
	}
}

func softwareHEVC() transcode.EncoderSupport {
	return transcode.EncoderSupport{Engine: transcode.Software, Codec: transcode.CodecHEVC, Encoder: "libx265", Working: true, Lookahead: true, TenBit: true, QualityMode: "crf"}
}

func buildInput(file transcode.File, target settings.QualityTarget, encoder transcode.EncoderSupport) transcoder.BuildInput {
	setup := settings.DefaultTranscodeSetup(target)
	return transcoder.BuildInput{
		File: file, Plan: transcoder.Analyze(file, target, setup, transcoder.WantedLanguages(target, nil)), Target: target, Setup: setup,
		Marker: "marker", Encoder: encoder, Input: "/in", Output: "/out",
	}
}

func TestBuildSpec_Example1CopiesVideoEncodesAudioAndKeepsOnlyAllowedTags(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "matroska,webm", Tags: map[string]string{"title": "Pilot", "encoder": "x"}, Streams: []transcode.Stream{
		video("hevc", 1920, 1080, 30, 2180), audio(1, "eac3", "en", 6, 48000, 640), audio(2, "aac", "hi", 2, 48000, 128),
	}}, target, 1.09)
	in := buildInput(file, target, softwareHEVC())
	in.Setup.Audio.Normalize = true
	in.File.Tags = map[string]string{"title": "Pilot", "encoder": "x", "SOLARR_TRANSCODED": "old"}
	assertSpec(t, transcoder.BuildSpec(in), transcode.ConvertSpec{
		Input: "/in", Output: "/out", Kind: transcode.Video, Container: "mp4", Engine: transcode.Software, Marker: "marker", CustomTags: true,
		Video: []transcode.VideoOut{{Index: 0, Copy: true}},
		Audio: []transcode.AudioOut{{Index: 1, Encoder: "aac", BitrateKbps: 128, Channels: 2, SampleRate: 48000, Language: "en", Default: true, Filters: []transcode.AudioFilter{{Kind: transcode.AudioNormalize}}}},
		Tags:  map[string]string{"title": "Pilot"},
	})
}

func TestBuildSpec_Example2QualityOnAGPUAddsTheOffsetClampsAndScales(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
		video("av1", 3840, 2160, 24, 9000), audio(1, "eac3", "en", 6, 48000, 640),
	}}, target, 2.0)
	encoder := transcode.EncoderSupport{Engine: transcode.QSV, Codec: transcode.CodecHEVC, Encoder: "hevc_qsv", Working: true, Lookahead: true, QualityMode: "icq"}
	in := buildInput(file, target, encoder)
	in.Device, in.FullGPU = "/dev/dri/d", true
	in.Setup.Encode.Quality, in.Setup.Encode.HardwareQualityOffset = 49, 4
	got := transcoder.BuildSpec(in)
	want := transcode.VideoOut{Index: 0, Encoder: "hevc_qsv", Preset: string(in.Setup.Encode.Preset), Width: 1920, Height: 1080,
		Rate: transcode.RateControl{Mode: transcode.RateModeQuality, Quality: 51, Lookahead: true, QualityMode: "icq"}}
	if !reflect.DeepEqual(got.Video, []transcode.VideoOut{want}) || got.Engine != transcode.QSV || got.Device != "/dev/dri/d" || !got.FullGPU {
		t.Errorf("video = %+v engine %s device %q full %v; want %+v", got.Video, got.Engine, got.Device, got.FullGPU, want)
	}
}

func TestBuildSpec_BitrateModeCapsAtTheSourceAndAPassOverridesIt(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{video("h264", 1920, 1080, 30, 1500), audio(1, "aac", "en", 2, 48000, 128)}}, target, 3)
	in := buildInput(file, target, softwareHEVC())
	in.Setup.Encode.RateMode = settings.RateBitrate
	in.Setup.Encode.VideoBitrateKbps = 4000
	in.Plan = transcoder.Analyze(file, target, in.Setup, transcoder.WantedLanguages(target, nil))
	if got := transcoder.BuildSpec(in).Video[0].Rate; got.Mode != transcode.RateModeBitrate || got.BitrateKbps != 1500 {
		t.Errorf("rate = %+v, want 1500 kbit/s: never above the source", got)
	}
	in.Pass.VideoBitrateKbps = 1200
	if got := transcoder.BuildSpec(in).Video[0].Rate.BitrateKbps; got != 1200 {
		t.Errorf("pass bitrate = %d, want 1200", got)
	}
	in.Pass.Scale = 0.5
	if got := transcoder.BuildSpec(in).Video[0].Rate.BitrateKbps; got != 600 {
		t.Errorf("scaled bitrate = %d, want 600", got)
	}
	in.Pass.Scale = 0
	in.Setup.Encode.RateMode, in.Setup.Encode.Quality, in.Pass.Quality = settings.RateQuality, 28, 31
	if got := transcoder.BuildSpec(in).Video[0].Rate; got.Mode != transcode.RateModeQuality || got.Quality != 31 {
		t.Errorf("pass quality = %+v, want 31", got)
	}
}

func TestBuildSpec_Example3FLACWithACoverGivesUpCustomTagsAndNormalisingDropsReplayGain(t *testing.T) {
	target := musicTarget()
	file := sized(transcode.File{Container: "flac", Tags: map[string]string{"musicbrainz_trackid": "x", "replaygain_track_gain": "-3 dB"}, Streams: []transcode.Stream{
		audio(0, "flac", "", 2, 96000, 1800), {Index: 1, Type: transcode.StreamVideo, Codec: "mjpeg", AttachedPicture: true},
	}}, target, 7)
	in := buildInput(file, target, softwareHEVC())
	in.Setup.Audio.Normalize = true
	got := transcoder.BuildSpec(in)
	assertSpec(t, got, transcode.ConvertSpec{
		Input: "/in", Output: "/out", Kind: transcode.Audio, Container: "m4a", Engine: transcode.Software, Marker: "marker",
		Audio:  []transcode.AudioOut{{Index: 0, Encoder: "aac", BitrateKbps: 128, Channels: 2, SampleRate: 48000, Default: true, Filters: []transcode.AudioFilter{{Kind: transcode.AudioNormalize}}}},
		Covers: []int{1}, Tags: map[string]string{"musicbrainz_trackid": "x"},
	})
}

func TestBuildSpec_FiltersFollowTheSetupAndDialogEnhanceNeedsAStereoSource(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
		{Index: 0, Type: transcode.StreamVideo, Codec: "h264", Width: 1920, Height: 1080, FrameRate: 25, BitDepth: 10, Interlaced: true, BitrateBps: 9000000}, audio(1, "ac3", "en", 6, 48000, 640),
	}}, target, 3)
	in := buildInput(file, target, softwareHEVC())
	in.Setup.Video = settings.VideoFilters{Deinterlace: true, Denoise: settings.StrengthMedium, DenoiseMethod: settings.DenoiseAuto, Deband: settings.StrengthLow, Sharpen: settings.StrengthStrong, SharpenMethod: settings.SharpenSmartBlur}
	in.Setup.Audio = settings.AudioFilters{Compress: true, DialogEnhance: true, Denoise: settings.StrengthLow, DenoiseMethod: settings.AudioDenoiseNLM, Declip: true}
	in.Plan = transcoder.Analyze(file, target, in.Setup, transcoder.WantedLanguages(target, nil))
	got := transcoder.BuildSpec(in)
	wantVideo := []transcode.VideoFilter{{Kind: transcode.FilterDeinterlace}, {Kind: transcode.FilterBM3D, Strength: "medium"}, {Kind: transcode.FilterDeband, Strength: "low"}, {Kind: transcode.FilterSmartBlur, Strength: "strong"}}
	if !reflect.DeepEqual(got.Video[0].Filters, wantVideo) || !got.Video[0].TenBit {
		t.Errorf("video = %+v, want filters %+v and ten bit", got.Video[0], wantVideo)
	}
	wantAudio := []transcode.AudioFilter{{Kind: transcode.AudioDeclip}, {Kind: transcode.AudioDenoiseNLM, Strength: "low"}, {Kind: transcode.AudioCompress}}
	if !reflect.DeepEqual(got.Audio[0].Filters, wantAudio) {
		t.Errorf("audio filters = %+v, want %+v: dialogue enhance only on a stereo source", got.Audio[0].Filters, wantAudio)
	}
}

func TestBuildSpec_HDRKeepPassesTheColourTagsAndHLGIsItsOwnTransfer(t *testing.T) {
	target := movieTarget()
	target.HDR = "hdr10"
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
		{Index: 0, Type: transcode.StreamVideo, Codec: "h264", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, HDR: "hlg", BitrateBps: 20000000}, audio(1, "aac", "en", 2, 48000, 128),
	}}, target, 3)
	got := transcoder.BuildSpec(buildInput(file, target, softwareHEVC())).Video[0]
	if got.HDR != transcode.HDRKeep || got.ColorTransfer != "arib-std-b67" || got.ColorPrimaries != "bt2020" || got.ColorSpace != "bt2020nc" {
		t.Errorf("video = %+v, want kept HLG tags", got)
	}
}
