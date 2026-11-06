package quality_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
)

func TestMeetsTarget_AVideoTargetNamesOnlyWhatItCaresAbout(t *testing.T) {
	file := score.FileFacts{Resolution: "1080p", VideoCodec: "h265", AudioCodec: "eac3", Extension: "mkv"}
	cases := []struct {
		name   string
		target settings.QualityTarget
		want   bool
	}{
		{"resolution and codec equal", settings.QualityTarget{Resolution: "1080p", VideoCodec: "h265"}, true},
		{"codec free", settings.QualityTarget{Resolution: "1080p"}, true},
		{"wrong resolution", settings.QualityTarget{Resolution: "2160p"}, false},
		{"wrong codec", settings.QualityTarget{Resolution: "1080p", VideoCodec: "h264"}, false},
		{"sdr target and a file without hdr", settings.QualityTarget{Resolution: "1080p", HDR: "sdr"}, true},
		{"hdr target and an sdr file", settings.QualityTarget{Resolution: "1080p", HDR: "hdr10"}, false},
		{"container named and different", settings.QualityTarget{Resolution: "1080p", Container: "mp4"}, false},
	}
	for _, c := range cases {
		if got := quality.MeetsTarget(c.target, file); got != c.want {
			t.Errorf("%s: MeetsTarget = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMeetsTarget_ChannelsLanguagesBitratesFrameRateAndSeveralFileTypes(t *testing.T) {
	file := score.FileFacts{
		Resolution: "1080p", VideoCodec: "h264", AudioChannels: "5.1", AudioLanguages: []string{"de", "en"}, SubtitleLanguages: []string{"en"},
		VideoBitrate: 4_200_000, AudioBitrate: 384_000, FrameRate: 23.976, Container: "avi",
	}
	four, audio := int64(4000), int64(384)
	cases := []struct {
		name   string
		target settings.QualityTarget
		want   bool
	}{
		{"one of several file types", settings.QualityTarget{Resolution: "1080p", Container: "mkv,avi,wmv"}, true},
		{"none of the file types", settings.QualityTarget{Resolution: "1080p", Container: "mkv,mp4"}, false},
		{"channels equal", settings.QualityTarget{AudioChannels: "5.1"}, true},
		{"channels differ", settings.QualityTarget{AudioChannels: "2.0"}, false},
		{"one wanted audio language present", settings.QualityTarget{AudioLanguages: []string{"fr", "de"}}, true},
		{"no wanted audio language present", settings.QualityTarget{AudioLanguages: []string{"fr"}}, false},
		{"subtitle language present", settings.QualityTarget{SubtitleLanguages: []string{"en"}}, true},
		{"video bitrate within the default margin", settings.QualityTarget{VideoBitrateKbps: &four}, true},
		{"audio bitrate within the margin", settings.QualityTarget{AudioBitrateKbps: &audio, TolerancePct: 5}, true},
		{"frame rate 24 is 23.976", settings.QualityTarget{FrameRate: 24}, true},
		{"frame rate 30 is not", settings.QualityTarget{FrameRate: 30}, false},
	}
	for _, c := range cases {
		if got := quality.MeetsTarget(c.target, file); got != c.want {
			t.Errorf("%s: MeetsTarget = %v, want %v", c.name, got, c.want)
		}
	}
	if !quality.MeetsTarget(settings.QualityTarget{AudioLanguages: []string{"fr"}}, score.FileFacts{Resolution: "1080p"}) {
		t.Error("a file whose languages the probe could not read was held against a language target")
	}
}

// A file at or below the target's CRF (the same quality or better), or within two points above it, meets it; one that states no CRF is not held against it.
func TestMeetsTarget_CRFIsAtOrBelowAndUnknownIsFree(t *testing.T) {
	target := settings.QualityTarget{CRF: 20}
	cases := []struct {
		name string
		crf  float64
		want bool
	}{
		{"better than the target", 18, true},
		{"exactly the target", 20, true},
		{"within the tolerance", 22, true},
		{"just past the tolerance", 22.5, false},
		{"worse than the target", 26, false},
		{"states none", 0, true},
	}
	for _, c := range cases {
		if got := quality.MeetsTarget(target, score.FileFacts{Resolution: "1080p", VideoCodec: "h264", CRF: c.crf}); got != c.want {
			t.Errorf("%s: MeetsTarget = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMeetsTarget_AudioSampleRateDepthChannelsAndAFrameRateCap(t *testing.T) {
	music := settings.QualityTarget{MediaType: "music", AudioCodec: "flac", Container: "flac", AudioChannels: "2.0", SampleRateHz: 48000, BitDepth: 16}
	file := score.FileFacts{AudioCodec: "flac", Extension: "flac", AudioChannels: "2.0", SampleRate: 48000, BitDepth: 16}
	if !quality.MeetsTarget(music, file) {
		t.Error("a 48 kHz 16-bit stereo flac must meet it")
	}
	for name, change := range map[string]func(*score.FileFacts){
		"rate": func(f *score.FileFacts) { f.SampleRate = 22050 }, "depth": func(f *score.FileFacts) { f.BitDepth = 8 },
		"channels": func(f *score.FileFacts) { f.AudioChannels = "5.1" },
	} {
		other := file
		change(&other)
		if quality.MeetsTarget(music, other) {
			t.Errorf("a file with a different %s must not meet it", name)
		}
	}
	for name, change := range map[string]func(*score.FileFacts){
		"44.1 kHz for a 48 kHz target":    func(f *score.FileFacts) { f.SampleRate = 44100 },
		"96 kHz 24-bit for 48 kHz 16-bit": func(f *score.FileFacts) { f.SampleRate, f.BitDepth = 96000, 24 },
	} {
		other := file
		change(&other)
		if !quality.MeetsTarget(music, other) {
			t.Errorf("%s must meet it: a transcode only lowers the rate", name)
		}
	}
	mono := settings.QualityTarget{MediaType: "audiobook", AudioCodec: "aac", Container: "m4b", AudioChannels: "1.0"}
	if !quality.MeetsTarget(mono, score.FileFacts{AudioCodec: "aac", Extension: "m4b", AudioChannels: "1.0"}) {
		t.Error("a mono audiobook target is an audio target, not a video one")
	}
	capped := settings.QualityTarget{MediaType: "movie", Resolution: "1080p", MaxFrameRate: 60}
	if !quality.MeetsTarget(capped, score.FileFacts{Resolution: "1080p", FrameRate: 59.94}) || quality.MeetsTarget(capped, score.FileFacts{Resolution: "1080p", FrameRate: 120}) {
		t.Error("a frame rate at or below the cap meets it, above does not")
	}
}

func TestDistance_IsAShareOfTheMatchedPartsAndSkipsWhatWasNotMeasured(t *testing.T) {
	target := settings.QualityTarget{MediaType: "movie", Resolution: "1080p", VideoCodec: "h265", Container: "mp4", CRF: 28, MaxFrameRate: 30}
	exact := score.FileFacts{Resolution: "1080p", VideoCodec: "h265", Extension: "mp4", CRF: 28, FrameRate: 29.97}
	if got, ok := quality.Distance(target, exact); !ok || got.Percent != 100 || len(got.Gaps()) != 0 {
		t.Errorf("an exact file = %+v ok %v, want 100%% and no gaps", got, ok)
	}
	// h264 at 720p: the codec part (weight 2) is lost, the resolution part (weight 2) loses 0.4, the rest match: 4.7 of 7.5.
	worse := score.FileFacts{Resolution: "720p", VideoCodec: "h264", Extension: "mp4", CRF: 28, FrameRate: 29.97}
	got, _ := quality.Distance(target, worse)
	if got.Percent != 63 {
		t.Errorf("a 720p h264 file = %d%%, want 63", got.Percent)
	}
	if gaps := got.Gaps(); len(gaps) != 2 || gaps[0].Dimension != "video codec" || gaps[1].Dimension != "resolution" {
		t.Errorf("gaps = %+v, want the codec then the resolution", gaps)
	}
	if _, ok := quality.Distance(target, score.FileFacts{}); ok {
		t.Error("a file nothing was read from has no closeness")
	}
	bitrate := settings.QualityTarget{MediaType: "movie", VideoBitrateKbps: new(int64)}
	*bitrate.VideoBitrateKbps = 1600
	if got, _ := quality.Distance(bitrate, score.FileFacts{VideoBitrate: 400_000}); got.Percent != 0 {
		t.Errorf("a quarter of the bitrate = %d%%, want 0", got.Percent)
	}
	for _, kbps := range []int64{1500, 1600, 6400} {
		if got, _ := quality.Distance(bitrate, score.FileFacts{VideoBitrate: kbps * 1000}); got.Percent != 100 {
			t.Errorf("%d kbit/s = %d%%, want 100: at, a little under or over the target is on it", kbps, got.Percent)
		}
	}
}

func TestMeetsTarget_ATranscodedFileNearTheBitrateIsOnTargetWhateverItsSize(t *testing.T) {
	bitrate, audio := int64(1600), int64(128)
	target := settings.QualityTarget{MediaType: "movie", Resolution: "1080p", VideoCodec: "h265", Container: "mp4", AudioCodec: "aac", AudioChannels: "2.0",
		FrameRate: 30, VideoBitrateKbps: &bitrate, AudioBitrateKbps: &audio}
	file := score.FileFacts{Resolution: "1080p", VideoCodec: "h265", Extension: "mp4", AudioCodec: "aac", AudioChannels: "2.0", FrameRate: 29.97}
	for name, rate := range map[string]int64{"on": 1600, "a bit under": 1100, "a constant-quality encode of a quiet film": 700, "far over, never transcoded": 9000} {
		file.VideoBitrate, file.AudioBitrate = rate*1000, 128_000
		if !quality.MeetsTarget(target, file) {
			t.Errorf("%s (%d kbit/s): a file with the target's container, codecs, resolution, rate and channels must meet it", name, rate)
		}
	}
	file.VideoBitrate = 400_000
	if quality.MeetsTarget(target, file) {
		t.Error("a quarter of the bitrate is short of the target")
	}
	file.VideoBitrate, file.VideoCodec = 1_600_000, "h264"
	if quality.MeetsTarget(target, file) {
		t.Error("a different codec is short of the target however good its bitrate")
	}
}

func TestMeetsTarget_AVideoBitrateFarUnderIsFineWhenTheCRFIsWithinTheTarget(t *testing.T) {
	bitrate := int64(1600)
	target := settings.QualityTarget{MediaType: "movie", Resolution: "480p", VideoCodec: "h265", Container: "mp4", CRF: 28, VideoBitrateKbps: &bitrate}
	file := score.FileFacts{Resolution: "480p", VideoCodec: "h265", Extension: "mp4", VideoBitrate: 300_000, CRF: 30}
	if !quality.MeetsTarget(target, file) {
		t.Error("a 480p file at CRF 30 and 300 kbit/s is the quality a CRF 28 target asks for")
	}
	if got, _ := quality.Distance(target, file); got.Percent != 100 {
		t.Errorf("closeness = %d%%, want 100: the CRF vouches for the low bitrate", got.Percent)
	}
	file.CRF = 34
	if quality.MeetsTarget(target, file) {
		t.Error("a CRF well past the tolerance must not meet the target, whatever its bitrate")
	}
	file.CRF = 0
	if quality.MeetsTarget(target, file) {
		t.Error("with no CRF to vouch for it, a quarter of the bitrate is short")
	}
}
