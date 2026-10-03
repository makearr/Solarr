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
