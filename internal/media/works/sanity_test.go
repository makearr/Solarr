package works

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

func TestRequirePlausibleSize(t *testing.T) {
	for _, test := range []struct {
		name       string
		resolution string
		bitrate    int64
		rejected   bool
	}{
		{"a stub claiming 1080p", "1080p", 400_000, true},
		{"a real 1080p encode", "1080p", 2_000_000, false},
		{"a small but real 720p", "720p", 350_000, false},
		{"an unmarked stub", "", 86_000, true},
		{"no estimate", "1080p", 0, false},
	} {
		facts := score.Facts{}
		facts.File.Resolution, facts.File.VideoBitrate = test.resolution, test.bitrate
		decision := model.Decision{Outcome: model.OutcomeAccept}
		requirePlausibleSize(&decision, facts)
		if got := decision.Outcome == model.OutcomeReject; got != test.rejected {
			t.Errorf("%s: rejected = %v, want %v", test.name, got, test.rejected)
		}
		if test.rejected && (len(decision.Rejections) != 1 || decision.Rejections[0].Rule != RuleImplausibleSize) {
			t.Errorf("%s: rejections = %+v", test.name, decision.Rejections)
		}
	}
}
