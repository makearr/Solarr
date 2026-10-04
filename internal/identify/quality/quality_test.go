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

// A file at or below the target's CRF (the same quality or better) meets it; one that states no CRF is not held against it.
func TestMeetsTarget_CRFIsAtOrBelowAndUnknownIsFree(t *testing.T) {
	target := settings.QualityTarget{CRF: 20}
	cases := []struct {
		name string
		crf  float64
		want bool
	}{
		{"better than the target", 18, true},
		{"exactly the target", 20, true},
		{"within the rounding margin", 20.4, true},
		{"worse than the target", 23, false},
		{"states none", 0, true},
	}
	for _, c := range cases {
		if got := quality.MeetsTarget(target, score.FileFacts{Resolution: "1080p", VideoCodec: "h264", CRF: c.crf}); got != c.want {
			t.Errorf("%s: MeetsTarget = %v, want %v", c.name, got, c.want)
		}
	}
}
