package transcoder_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func ptr(value int64) *int64 { return &value }

// movieTarget is the user's example target: mp4, 1080p 30 fps h265, stereo AAC 128 kbit/s 48 kHz, English or Japanese.
func movieTarget() settings.QualityTarget {
	return settings.QualityTarget{
		Name: "Film", MediaType: "video", Container: "mp4", Resolution: "1080p", VideoCodec: "h265", FrameRate: 30, MaxFrameRate: 30,
		AudioCodec: "aac", AudioChannels: "2.0", SampleRateHz: 48000, AudioBitrateKbps: ptr(128), VideoBitrateKbps: ptr(2000),
		AudioLanguages: []string{"en", "ja"},
	}
}

func musicTarget() settings.QualityTarget {
	return settings.QualityTarget{Name: "Songs", MediaType: "music", Container: "m4a", AudioCodec: "aac", AudioChannels: "2.0", SampleRateHz: 48000, BitrateKbps: ptr(128)}
}

func analyze(file transcode.File, target settings.QualityTarget) transcoder.Plan {
	return transcoder.Analyze(file, target, settings.DefaultTranscodeSetup(target), transcoder.WantedLanguages(target, nil))
}

// sized gives the file the overall bitrate that is ratio times what the target expects.
func sized(file transcode.File, target settings.QualityTarget, ratio float64) transcode.File {
	expected, _ := quality.ExpectedKbps(target)
	file.Duration = 6000 * time.Second
	file.SizeBytes = int64(expected * ratio * 1000 / 8 * file.Duration.Seconds())
	return file
}

func video(codec string, width, height int, fps float64, kbps int64) transcode.Stream {
	return transcode.Stream{Index: 0, Type: transcode.StreamVideo, Codec: codec, Width: width, Height: height, FrameRate: fps, BitrateBps: kbps * 1000, BitDepth: 8}
}

func audio(index int, codec, language string, channels, rate int, kbps int64) transcode.Stream {
	return transcode.Stream{Index: index, Type: transcode.StreamAudio, Codec: codec, Language: language, Channels: channels, SampleRate: rate, BitrateBps: kbps * 1000}
}

func assertPlan(t *testing.T, got, want transcoder.Plan) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("plan = %s\nwant   %s", gotJSON, wantJSON)
	}
}

func TestAnalyze_Example1_CopiesVideoReencodesAudioDropsHindiAndRemuxesToMP4(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
		video("hevc", 1920, 1080, 30, 2180), audio(1, "eac3", "en", 6, 48000, 640), audio(2, "aac", "hi", 2, 48000, 128),
	}}, target, 1.09)
	assertPlan(t, analyze(file, target), transcoder.Plan{
		Verdict: transcoder.Needed, Container: "mp4", EncodesAudio: true,
		Reasons: []transcoder.Reason{
			{Code: transcoder.ReasonContainer, Stream: -1, Want: "mp4", Have: "matroska"},
			{Code: transcoder.ReasonAudioCodec, Stream: 1, Want: "aac", Have: "eac3"},
			{Code: transcoder.ReasonChannels, Stream: 1, Want: "2", Have: "6"},
			{Code: transcoder.ReasonUnwantedAudio, Stream: 2, Want: "en,ja", Have: "hi"},
		},
		Video:   []transcoder.StreamPlan{{Index: 0, Type: transcode.StreamVideo, Action: transcoder.ActionCopy, Codec: "hevc", Width: 1920, Height: 1080, FPS: 30, BitDepth: 8}},
		Audio:   []transcoder.StreamPlan{{Index: 1, Type: transcode.StreamAudio, Action: transcoder.ActionEncode, Codec: "aac", Channels: 2, SampleRate: 48000, BitrateKbps: 128, Language: "en", Default: true}},
		Dropped: []transcoder.StreamPlan{{Index: 2, Type: transcode.StreamAudio, Action: transcoder.ActionDrop, Reason: transcoder.ReasonUnwantedAudio, Codec: "aac", Language: "hi"}},
	})
}

func TestAnalyze_Example2_ReencodesVideoAndAudioOf4KAV1ToTheTarget(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
		video("av1", 3840, 2160, 24, 9000), audio(1, "eac3", "en", 6, 48000, 640),
	}}, target, 2.0)
	assertPlan(t, analyze(file, target), transcoder.Plan{
		Verdict: transcoder.Needed, Container: "mp4", EncodesVideo: true, EncodesAudio: true,
		Reasons: []transcoder.Reason{
			{Code: transcoder.ReasonContainer, Stream: -1, Want: "mp4", Have: "matroska"},
			{Code: transcoder.ReasonVideoCodec, Stream: 0, Want: "h265", Have: "av1"},
			{Code: transcoder.ReasonAudioCodec, Stream: 1, Want: "aac", Have: "eac3"},
			{Code: transcoder.ReasonResolution, Stream: 0, Want: "1080p", Have: "2160p"},
			{Code: transcoder.ReasonChannels, Stream: 1, Want: "2", Have: "6"},
			{Code: transcoder.ReasonAboveRange, Stream: -1, Want: "120", Have: "200"},
		},
		Video: []transcoder.StreamPlan{{Index: 0, Type: transcode.StreamVideo, Action: transcoder.ActionEncode, Codec: "h265", Width: 1920, Height: 1080, FPS: 24, BitDepth: 8}},
		Audio: []transcoder.StreamPlan{{Index: 1, Type: transcode.StreamAudio, Action: transcoder.ActionEncode, Codec: "aac", Channels: 2, SampleRate: 48000, BitrateKbps: 128, Language: "en", Default: true}},
	})
}

func TestAnalyze_Example3_ConvertsA96kHzFLACToStereoAACInM4A(t *testing.T) {
	target := musicTarget()
	file := sized(transcode.File{Container: "flac", Streams: []transcode.Stream{audio(0, "flac", "", 2, 96000, 1800)}}, target, 7)
	plan := analyze(file, target)
	assertPlan(t, plan, transcoder.Plan{
		Verdict: transcoder.Needed, Container: "m4a", EncodesAudio: true,
		Reasons: []transcoder.Reason{
			{Code: transcoder.ReasonContainer, Stream: -1, Want: "m4a", Have: "flac"},
			{Code: transcoder.ReasonAudioCodec, Stream: 0, Want: "aac", Have: "flac"},
			{Code: transcoder.ReasonSampleRate, Stream: 0, Want: "48000", Have: "96000"},
			{Code: transcoder.ReasonAboveRange, Stream: -1, Want: "120", Have: "700"},
		},
		Audio: []transcoder.StreamPlan{{Index: 0, Type: transcode.StreamAudio, Action: transcoder.ActionEncode, Codec: "aac", Channels: 2, SampleRate: 48000, BitrateKbps: 128, Default: true}},
	})
	if got := plan.Lane(transcode.Software); got != transcoder.LaneAudio {
		t.Errorf("lane = %s, want audio", got)
	}
}

func TestAnalyze_Example4_ReencodesH264At60PercentToH265AndNotesBelowRange(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{
		video("h264", 1280, 720, 30, 1200), audio(1, "aac", "en", 2, 48000, 128),
	}}, target, 0.6)
	assertPlan(t, analyze(file, target), transcoder.Plan{
		Verdict: transcoder.Needed, Container: "mp4", EncodesVideo: true,
		Reasons:  []transcoder.Reason{{Code: transcoder.ReasonVideoCodec, Stream: 0, Want: "h265", Have: "h264"}},
		Warnings: []transcoder.Reason{{Code: transcoder.ReasonBelowRange, Stream: -1, Have: "60"}},
		Video:    []transcoder.StreamPlan{{Index: 0, Type: transcode.StreamVideo, Action: transcoder.ActionEncode, Codec: "h265", Width: 1280, Height: 720, FPS: 30, BitDepth: 8}},
		Audio:    []transcoder.StreamPlan{{Index: 1, Type: transcode.StreamAudio, Action: transcoder.ActionCopy, Codec: "aac", Channels: 2, SampleRate: 48000, BitrateKbps: 128, Language: "en", Default: true}},
	})
}

func TestAnalyze_Example5_AH265FileAt50PercentWithRightAudioIsNotNeeded(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{
		video("hevc", 1280, 720, 30, 900), audio(1, "aac", "ja", 2, 48000, 128),
	}}, target, 0.5)
	assertPlan(t, analyze(file, target), transcoder.Plan{Verdict: transcoder.NotNeeded, Warnings: []transcoder.Reason{{Code: transcoder.ReasonBelowRange, Stream: -1, Have: "50"}}})
}

func TestFileFromMeasured_GivesTheSameVerdictAsTheFullFileForTheFiveExamples(t *testing.T) {
	movie, music := movieTarget(), musicTarget()
	cases := []struct {
		name      string
		target    settings.QualityTarget
		full      transcode.File
		measured  mediatool.Measured
		container string
	}{
		{"example 1", movie, sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{video("hevc", 1920, 1080, 30, 2180), audio(1, "eac3", "en", 6, 48000, 640), audio(2, "eac3", "hi", 6, 48000, 128)}}, movie, 1.09),
			mediatool.Measured{VideoCodec: "hevc", Width: 1920, Height: 1080, FrameRate: 30, VideoBitrate: 2180000, AudioCodec: "eac3", Channels: 6, SampleRate: 48000, AudioBitrate: 640000, AudioLanguages: []string{"en", "hi"}}, "matroska,webm"},
		{"example 2", movie, sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{video("av1", 3840, 2160, 24, 9000), audio(1, "eac3", "en", 6, 48000, 640)}}, movie, 2),
			mediatool.Measured{VideoCodec: "av1", Width: 3840, Height: 2160, FrameRate: 24, AudioCodec: "eac3", Channels: 6, SampleRate: 48000, AudioLanguages: []string{"en"}}, "matroska,webm"},
		{"example 3", music, sized(transcode.File{Container: "flac", Streams: []transcode.Stream{audio(0, "flac", "", 2, 96000, 1800)}}, music, 7),
			mediatool.Measured{AudioCodec: "flac", Channels: 2, SampleRate: 96000, AudioBitrate: 1800000}, "flac"},
		{"example 4", movie, sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{video("h264", 1280, 720, 30, 1200), audio(1, "aac", "en", 2, 48000, 128)}}, movie, 0.6),
			mediatool.Measured{VideoCodec: "h264", Width: 1280, Height: 720, FrameRate: 30, AudioCodec: "aac", Channels: 2, SampleRate: 48000, AudioLanguages: []string{"en"}}, "mov,mp4,m4a,3gp,3g2,mj2"},
		{"example 5", movie, sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{video("hevc", 1280, 720, 30, 900), audio(1, "aac", "ja", 2, 48000, 128)}}, movie, 0.5),
			mediatool.Measured{VideoCodec: "hevc", Width: 1280, Height: 720, FrameRate: 30, AudioCodec: "aac", Channels: 2, SampleRate: 48000, AudioLanguages: []string{"ja"}}, "mov,mp4,m4a,3gp,3g2,mj2"},
	}
	for _, c := range cases {
		c.measured.Duration, c.measured.OverallBitrate = c.full.Duration, c.full.SizeBytes*8/int64(c.full.Duration.Seconds())
		coarse := transcoder.FileFromMeasured(c.measured, c.container, c.full.SizeBytes, nil)
		if got, want := analyze(coarse, c.target).Verdict, analyze(c.full, c.target).Verdict; got != want {
			t.Errorf("%s: coarse verdict %s, full %s", c.name, got, want)
		}
	}
}
