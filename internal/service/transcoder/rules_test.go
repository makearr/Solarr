package transcoder_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func codes(reasons []transcoder.Reason) []transcoder.ReasonCode {
	var out []transcoder.ReasonCode
	for _, reason := range reasons {
		out = append(out, reason.Code)
	}
	return out
}

// onTarget is a movie file that passes every rule at the target's bitrate.
func onTarget(target settings.QualityTarget) transcode.File {
	return sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{
		video("hevc", 1920, 1080, 30, 1900), audio(1, "aac", "en", 2, 48000, 128),
	}}, target, 1.0)
}

func TestAnalyze_AFileOnTargetIsNotNeeded(t *testing.T) {
	target := movieTarget()
	if plan := analyze(onTarget(target), target); plan.Verdict != transcoder.NotNeeded || len(plan.Reasons) != 0 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestAnalyze_EachSkipRuleFailsOnItsOwnAndSwitchesOff(t *testing.T) {
	target := movieTarget()
	cases := []struct {
		name   string
		change func(*transcode.File)
		off    func(*settings.TranscodeSkip)
		code   transcoder.ReasonCode
	}{
		{"container", func(f *transcode.File) { f.Container = "matroska,webm" }, func(s *settings.TranscodeSkip) { s.ContainerMatches = false }, transcoder.ReasonContainer},
		{"video codec", func(f *transcode.File) { f.Streams[0].Codec = "h264" }, func(s *settings.TranscodeSkip) { s.CodecMatches = false }, transcoder.ReasonVideoCodec},
		{"audio codec", func(f *transcode.File) { f.Streams[1].Codec = "ac3" }, func(s *settings.TranscodeSkip) { s.CodecMatches = false }, transcoder.ReasonAudioCodec},
		{"resolution", func(f *transcode.File) { f.Streams[0].Width, f.Streams[0].Height = 3840, 2160 }, func(s *settings.TranscodeSkip) { s.ResolutionAtOrBelow = false }, transcoder.ReasonResolution},
		{"frame rate", func(f *transcode.File) { f.Streams[0].FrameRate = 60 }, func(s *settings.TranscodeSkip) { s.RateAtOrBelow = false }, transcoder.ReasonFrameRate},
		{"sample rate", func(f *transcode.File) { f.Streams[1].SampleRate = 96000 }, func(s *settings.TranscodeSkip) { s.RateAtOrBelow = false }, transcoder.ReasonSampleRate},
		{"channels", func(f *transcode.File) { f.Streams[1].Channels = 6 }, func(s *settings.TranscodeSkip) { s.RateAtOrBelow = false }, transcoder.ReasonChannels},
		{"audio language", func(f *transcode.File) { f.Streams = append(f.Streams, audio(2, "aac", "fr", 2, 48000, 96)) }, func(s *settings.TranscodeSkip) { s.OnlyWantedLanguages = false }, transcoder.ReasonUnwantedAudio},
		{"subtitle language", func(f *transcode.File) {
			f.Streams = append(f.Streams, transcode.Stream{Index: 2, Type: transcode.StreamSubtitle, Codec: "subrip", Language: "fr"})
		}, func(s *settings.TranscodeSkip) { s.OnlyWantedLanguages = false }, transcoder.ReasonUnwantedSubtitle},
		{"range", func(f *transcode.File) { f.SizeBytes *= 3; f.Streams[0].BitrateBps = 3000000 }, func(s *settings.TranscodeSkip) { s.WithinRange = false }, transcoder.ReasonAboveRange},
	}
	subtitled := target
	subtitled.SubtitleLanguages = []string{"en"}
	for _, c := range cases {
		file := onTarget(target)
		c.change(&file)
		wanted := target
		if c.name == "subtitle language" {
			wanted = subtitled
		}
		setup := settings.DefaultTranscodeSetup(wanted)
		if got := transcoder.Analyze(file, wanted, setup, transcoder.WantedLanguages(wanted, nil)); !slices.Contains(codes(got.Reasons), c.code) {
			t.Errorf("%s: reasons %v lack %s", c.name, codes(got.Reasons), c.code)
		}
		c.off(&setup.Skip)
		if got := transcoder.Analyze(file, wanted, setup, transcoder.WantedLanguages(wanted, nil)); slices.Contains(codes(got.Reasons), c.code) {
			t.Errorf("%s: still fires with its switch off: %v", c.name, codes(got.Reasons))
		}
	}
}

func TestAnalyze_TheMaxRuleIsOffByDefaultAndFiresAbove120PercentWhenOnWithARangeThatPasses(t *testing.T) {
	target := movieTarget()
	file := sized(onTarget(target), target, 1.1)
	file.Streams[0].BitrateBps = 3000000
	setup := settings.DefaultTranscodeSetup(target)
	setup.Skip.BelowMax, setup.Skip.MaxPct = true, 105
	got := transcoder.Analyze(file, target, setup, transcoder.WantedLanguages(target, nil))
	if want := []transcoder.ReasonCode{transcoder.ReasonAboveMax}; !slices.Equal(codes(got.Reasons), want) {
		t.Errorf("reasons = %v, want %v", codes(got.Reasons), want)
	}
}

func TestAnalyze_BelowTheRangeNeverTriggersAConversionAndTargetsWithoutABitrateWarnRangeUnknown(t *testing.T) {
	target := movieTarget()
	if plan := analyze(sized(onTarget(target), target, 0.3), target); plan.Verdict != transcoder.NotNeeded || codes(plan.Warnings)[0] != transcoder.ReasonBelowRange {
		t.Errorf("30 percent = %+v", plan)
	}
	if plan := analyze(sized(onTarget(target), target, 3), target); plan.Verdict != transcoder.NotNeeded || codes(plan.Reasons)[0] != transcoder.ReasonNothingToDo {
		t.Errorf("300 percent with every stream on target has nothing to do: %+v", plan)
	}
	unknown := target
	unknown.VideoBitrateKbps = nil
	plan := analyze(onTarget(target), unknown)
	if plan.Verdict != transcoder.NotNeeded || !slices.Equal(codes(plan.Warnings), []transcoder.ReasonCode{transcoder.ReasonRangeUnknown}) {
		t.Errorf("no target bitrate = %+v", plan)
	}
}

func TestAnalyze_AFileSolarrConvertedBeforeIsNotNeededUnlessThatRuleIsOff(t *testing.T) {
	target := movieTarget()
	file := onTarget(target)
	file.Container, file.Tags = "matroska,webm", map[string]string{transcoder.MarkerTag: "1"}
	if plan := analyze(file, target); !slices.Equal(codes(plan.Reasons), []transcoder.ReasonCode{transcoder.ReasonTranscodedBefore}) || plan.Verdict != transcoder.NotNeeded {
		t.Errorf("plan = %+v", plan)
	}
	setup := settings.DefaultTranscodeSetup(target)
	setup.Skip.TranscodedBefore = false
	if plan := transcoder.Analyze(file, target, setup, transcoder.WantedLanguages(target, nil)); plan.Verdict != transcoder.Needed {
		t.Errorf("rule off: %+v", plan)
	}
}

func TestAnalyze_TheUpSwitchesOffBringAStreamUpToTheTarget(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{
		video("h264", 1280, 720, 24, 1200), audio(1, "aac", "en", 1, 44100, 64),
	}}, target, 1.0)
	setup := settings.DefaultTranscodeSetup(target)
	plan := transcoder.Analyze(file, target, setup, transcoder.WantedLanguages(target, nil))
	if v, a := plan.Video[0], plan.Audio[0]; v.Width != 1280 || v.FPS != 24 || a.Channels != 0 && a.Channels != 1 || a.Action != transcoder.ActionCopy && a.SampleRate != 44100 {
		t.Errorf("by default nothing grows: %+v %+v", v, a)
	}
	setup.Streams.NoUpscale, setup.Streams.NoFPSIncrease, setup.Streams.NoUpmix, setup.Streams.NoSampleRateIncrease = false, false, false, false
	setup.Streams.CopyAudioOnTarget = false
	plan = transcoder.Analyze(file, target, setup, transcoder.WantedLanguages(target, nil))
	if v, a := plan.Video[0], plan.Audio[0]; v.Width != 1920 || v.Height != 1080 || v.FPS != 30 || a.Channels != 2 || a.SampleRate != 48000 {
		t.Errorf("switched off, streams reach the target: %+v %+v", v, a)
	}
}

func TestAnalyze_DownscaleOffKeepsALargePictureAndReduceFPSHalves60(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{video("h264", 3840, 2160, 59.94, 9000), audio(1, "aac", "en", 2, 48000, 128)}}, target, 1)
	setup := settings.DefaultTranscodeSetup(target)
	if got := transcoder.Analyze(file, target, setup, transcoder.Languages{}).Video[0]; got.Width != 1920 || got.FPS < 29.96 || got.FPS > 29.98 {
		t.Errorf("scaled = %+v", got)
	}
	setup.Streams.Downscale, setup.Streams.ReduceFPS = false, false
	if got := transcoder.Analyze(file, target, setup, transcoder.Languages{}).Video[0]; got.Width != 3840 || got.FPS != 59.94 {
		t.Errorf("kept = %+v", got)
	}
}

func TestAnalyze_AudioKeepsTheBestTrackPerWantedLanguageAndAllUntaggedOnes(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
		video("hevc", 1920, 1080, 30, 1900),
		audio(1, "ac3", "en", 6, 48000, 448),
		{Index: 2, Type: transcode.StreamAudio, Codec: "aac", Language: "en", Channels: 2, SampleRate: 48000, BitrateBps: 96000, Commentary: true},
		audio(3, "aac", "en", 2, 48000, 128),
		audio(4, "aac", "", 2, 48000, 128),
		audio(5, "aac", "ja", 2, 48000, 128),
		audio(6, "aac", "de", 2, 48000, 128),
	}}, target, 1)
	plan := analyze(file, target)
	var kept []int
	for _, stream := range plan.Audio {
		kept = append(kept, stream.Index)
	}
	if want := []int{3, 5, 4}; !slices.Equal(kept, want) || !plan.Audio[0].Default || plan.Audio[1].Default {
		t.Errorf("kept = %v, want %v (best English, Japanese, then untagged)", kept, want)
	}
	reasons := map[int]transcoder.ReasonCode{}
	for _, stream := range plan.Dropped {
		reasons[stream.Index] = stream.Reason
	}
	want := map[int]transcoder.ReasonCode{1: transcoder.ReasonDuplicateLanguage, 2: transcoder.ReasonCommentary, 6: transcoder.ReasonUnwantedAudio}
	if len(reasons) != 3 || reasons[1] != want[1] || reasons[2] != want[2] || reasons[6] != want[6] {
		t.Errorf("dropped = %v, want %v", reasons, want)
	}
}

func TestAnalyze_WithNoWantedLanguageInTheFileOrNoneWantedEveryTrackStays(t *testing.T) {
	target := movieTarget()
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
		video("hevc", 1920, 1080, 30, 1900), audio(1, "aac", "de", 2, 48000, 128), audio(2, "aac", "fr", 2, 48000, 128),
	}}, target, 1)
	if plan := analyze(file, target); len(plan.Audio) != 2 || len(plan.Dropped) != 0 {
		t.Errorf("no wanted language present: %+v", plan)
	}
	open := target
	open.AudioLanguages = nil
	if plan := analyze(file, open); len(plan.Audio) != 2 || len(plan.Dropped) != 0 {
		t.Errorf("no language wanted: %+v", plan)
	}
}

func TestAnalyze_SubtitlesPictureOnesNeedMatroskaTextOnesAreConvertedForMP4AndFontsFollowAss(t *testing.T) {
	target := movieTarget()
	build := func() transcode.File {
		return sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{
			video("hevc", 1920, 1080, 30, 1900), audio(1, "ac3", "en", 2, 48000, 128),
			{Index: 2, Type: transcode.StreamSubtitle, Codec: "hdmv_pgs_subtitle", Language: "en", Picture: true},
			{Index: 3, Type: transcode.StreamSubtitle, Codec: "ass", Language: "en", Forced: true},
			{Index: 4, Type: transcode.StreamAttachment, Codec: "ttf"},
			{Index: 5, Type: transcode.StreamData, Codec: "bin_data"},
			{Index: 6, Type: transcode.StreamUnknown, Codec: "unknown"},
		}}, target, 1)
	}
	mp4 := analyze(build(), target)
	if len(mp4.Subtitles) != 1 || mp4.Subtitles[0].Index != 3 || mp4.Subtitles[0].Action != transcoder.ActionConvert || mp4.Subtitles[0].Codec != "mov_text" || !mp4.Subtitles[0].Forced {
		t.Errorf("mp4 subtitles = %+v", mp4.Subtitles)
	}
	dropped := map[int]transcoder.ReasonCode{}
	for _, stream := range mp4.Dropped {
		dropped[stream.Index] = stream.Reason
	}
	if dropped[2] != transcoder.ReasonPictureSubtitle || dropped[4] != transcoder.ReasonAttachment || dropped[5] != transcoder.ReasonDataStream || dropped[6] != transcoder.ReasonUnknownStream {
		t.Errorf("mp4 dropped = %v", dropped)
	}
	mkv := target
	mkv.Container = "mkv"
	plan := analyze(build(), mkv)
	if len(plan.Subtitles) != 2 || plan.Subtitles[0].Action != transcoder.ActionCopy || plan.Subtitles[1].Action != transcoder.ActionCopy || len(plan.Other) != 1 || plan.Other[0].Index != 4 || plan.Other[0].Action != transcoder.ActionCopy {
		t.Errorf("mkv: subtitles %+v other %+v", plan.Subtitles, plan.Other)
	}
}

func TestAnalyze_OnlyTheLargestVideoStreamStaysAndACoverIsNotVideo(t *testing.T) {
	target := movieTarget()
	small := video("hevc", 640, 360, 30, 300)
	small.Index = 2
	cover := transcode.Stream{Index: 3, Type: transcode.StreamVideo, Codec: "mjpeg", Width: 600, Height: 600, AttachedPicture: true}
	file := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{video("hevc", 1920, 1080, 30, 1900), audio(1, "aac", "en", 2, 48000, 128), small, cover}}, target, 1)
	plan := analyze(file, target)
	if len(plan.Video) != 1 || plan.Video[0].Index != 0 || len(plan.Dropped) != 1 || plan.Dropped[0].Reason != transcoder.ReasonExtraVideoStream || len(plan.Other) != 1 || plan.Other[0].Index != 3 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestAnalyze_BlockedFilesCarryTheirReason(t *testing.T) {
	target := movieTarget()
	dolby := sized(transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{func() transcode.Stream {
		stream := video("hevc", 3840, 2160, 24, 9000)
		stream.HDR, stream.DolbyVisionProfile = "dolby-vision", 5
		return stream
	}(), audio(1, "aac", "en", 2, 48000, 128)}}, target, 3)
	if plan := analyze(dolby, target); plan.Verdict != transcoder.Blocked || plan.Reasons[0].Code != transcoder.ReasonDolbyVisionProfile5 {
		t.Errorf("dolby vision 5 = %+v", plan)
	}
	book := settings.QualityTarget{MediaType: "book", Container: "epub"}
	if plan := analyze(onTarget(target), book); plan.Verdict != transcoder.Blocked || plan.Reasons[0].Code != transcoder.ReasonUnsupportedTarget {
		t.Errorf("book = %+v", plan)
	}
	empty := transcode.File{Container: "matroska,webm", Streams: []transcode.Stream{{Index: 0, Type: transcode.StreamSubtitle, Codec: "subrip"}}}
	if plan := analyze(empty, target); plan.Verdict != transcoder.Blocked || plan.Reasons[0].Code != transcoder.ReasonNoStreams {
		t.Errorf("no media streams = %+v", plan)
	}
}

func TestAnalyze_SDRTargetsTonemapHDRAndOthersKeepIt(t *testing.T) {
	target := movieTarget()
	hdr := video("hevc", 1920, 1080, 30, 1900)
	hdr.HDR, hdr.BitDepth = "hdr10", 10
	file := sized(transcode.File{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []transcode.Stream{hdr, audio(1, "aac", "en", 2, 48000, 128)}}, target, 1)
	kept := analyze(file, target)
	if kept.Verdict != transcoder.NotNeeded {
		t.Errorf("an HDR file on an unconstrained target is fine: %+v", kept)
	}
	sdr := target
	sdr.HDR = "sdr"
	plan := analyze(file, sdr)
	if plan.Verdict != transcoder.Needed || plan.Video[0].HDR != transcoder.HDRTonemap || plan.Video[0].Action != transcoder.ActionEncode {
		t.Errorf("sdr target = %+v", plan)
	}
}
