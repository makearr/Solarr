package works

import (
	"fmt"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

// RuleImplausibleSize is the rejection of a release whose size cannot hold the picture its name claims.
const RuleImplausibleSize = "implausible-size"

// plausibleVideoBitrate is the least video bitrate (bits/s) a real encode of that resolution has; below it the title
// is a mislabelled sample, a stub or a fake.
func plausibleVideoBitrate(resolution string) int64 {
	switch resolution {
	case "2160p", "1080p", "1080i":
		return 500_000
	case "720p":
		return 300_000
	default:
		return 100_000
	}
}

// requirePlausibleSize rejects an accepted release whose estimated video bitrate is below what its resolution can hold;
// a release with no estimate (no runtime, or not video) is left alone.
func requirePlausibleSize(decision *model.Decision, facts score.Facts) {
	if decision.Outcome != model.OutcomeAccept || facts.File.VideoBitrate == 0 {
		return
	}
	least := plausibleVideoBitrate(facts.File.Resolution)
	if facts.File.VideoBitrate >= least {
		return
	}
	decision.Outcome = model.OutcomeReject
	decision.Rejections = append(decision.Rejections, model.Rejection{
		Rule: RuleImplausibleSize, Field: "video_bitrate",
		Wanted: fmt.Sprintf("at least %d kbps for %s", least/1000, resolutionLabel(facts.File.Resolution)),
		Got:    fmt.Sprintf("about %d kbps", facts.File.VideoBitrate/1000),
	})
}

func resolutionLabel(resolution string) string {
	if resolution == "" {
		return "any video"
	}
	return resolution
}
