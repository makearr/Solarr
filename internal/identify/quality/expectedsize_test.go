package quality_test

import (
	"math"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
)

func ptr(value int64) *int64 { return &value }

func TestExpectedKbps(t *testing.T) {
	video := settings.QualityTarget{MediaType: "movie", Resolution: "1080p", VideoBitrateKbps: ptr(1500), AudioBitrateKbps: ptr(128)}
	got, ok := quality.ExpectedKbps(video)
	if !ok || math.Round(got) != 1652 {
		t.Fatalf("video target = %v, %v; want 1652 (1628 plus 1.5%% container)", got, ok)
	}
	withSubtitles := video
	withSubtitles.SubtitleLanguages = []string{"en", "de"}
	if got, _ := quality.ExpectedKbps(withSubtitles); math.Round(got) != 1654 {
		t.Errorf("two subtitle tracks = %v, want 1654", got)
	}
	if _, ok := quality.ExpectedKbps(settings.QualityTarget{MediaType: "movie", Resolution: "1080p", CRF: 22}); ok {
		t.Error("a target with no bitrate gave an expected size")
	}
}

func TestSizeRatio(t *testing.T) {
	// Food Boy: 6.8 GB where 1.1 GB is on target is 618%, not the 53% below a log scale gave.
	target := settings.QualityTarget{MediaType: "movie", VideoBitrateKbps: ptr(1500), AudioBitrateKbps: ptr(128)}
	expected, _ := quality.ExpectedKbps(target)
	ratio, _, ok := quality.SizeRatio(target, expected*6.18)
	if !ok || math.Round(ratio*100) != 618 {
		t.Errorf("ratio = %v, %v; want 6.18", ratio, ok)
	}
	if _, _, ok := quality.SizeRatio(target, 0); ok {
		t.Error("an unread bitrate gave a ratio")
	}
}

func TestOffIsTheSameForAFactorAndItsInverse(t *testing.T) {
	for _, pair := range [][2]float64{{0.5, 2}, {0.25, 4}, {0.9, 1 / 0.9}} {
		if a, b := quality.Off(pair[0]), quality.Off(pair[1]); math.Abs(a-b) > 1e-9 {
			t.Errorf("Off(%v) = %v but Off(%v) = %v", pair[0], a, pair[1], b)
		}
	}
	if got := quality.Off(2); got != 0.5 {
		t.Errorf("Off(2) = %v, want 0.5", got)
	}
}

func TestFits(t *testing.T) {
	target := settings.QualityTarget{
		MediaType: "movie", Resolution: "1080p", VideoCodec: "h265", AudioCodec: "aac", AudioChannels: "5.1",
		AudioLanguages: []string{"en"}, SubtitleLanguages: []string{"en"},
	}
	cases := []struct {
		name string
		file score.FileFacts
		want quality.TagFits
	}{
		{"on target", score.FileFacts{Resolution: "1080p", VideoCodec: "h265", AudioCodec: "aac", AudioChannels: "5.1", AudioLanguages: []string{"en"}, SubtitleLanguages: []string{"en"}},
			quality.TagFits{Resolution: quality.FitOK, VideoCodec: quality.FitOK, AudioCodec: quality.FitOK, AudioLanguage: quality.FitOK, SubtitleLanguage: quality.FitOK}},
		{"960 lines is a bit off", score.FileFacts{Resolution: "720p", Height: 960}, quality.TagFits{Resolution: quality.FitNear}},
		{"720p is far off", score.FileFacts{Resolution: "720p", Height: 720}, quality.TagFits{Resolution: quality.FitOff}},
		{"right codec, other channels", score.FileFacts{AudioCodec: "aac", AudioChannels: "2.0"}, quality.TagFits{AudioCodec: quality.FitNear}},
		{"wrong codec", score.FileFacts{VideoCodec: "h264", AudioCodec: "mp3"}, quality.TagFits{VideoCodec: quality.FitOff, AudioCodec: quality.FitOff}},
		{"wrong languages", score.FileFacts{AudioLanguages: []string{"de"}, SubtitleLanguages: []string{"fr"}}, quality.TagFits{AudioLanguage: quality.FitOff, SubtitleLanguage: quality.FitOff}},
		{"no subtitles at all is not red", score.FileFacts{AudioLanguages: []string{"en"}}, quality.TagFits{AudioLanguage: quality.FitOK}},
	}
	for _, test := range cases {
		if got := quality.Fits(target, test.file); got != test.want {
			t.Errorf("%s: %+v, want %+v", test.name, got, test.want)
		}
	}
}
