package judge_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
)

func TestWithTarget_TheTargetsResolutionAndCodecLeadTheRanking(t *testing.T) {
	rules := score.Profile{
		Compare: []string{"resolution", "source", "score"},
		Tiers: map[string][][]string{
			"resolution": {{"2160p"}, {"1080p", "1080i"}, {"720p"}},
			"source":     {{"webdl"}, {"hdtv"}},
		},
	}
	got := judge.WithTarget(rules, settings.QualityTarget{Resolution: "1080p", VideoCodec: "h265"})
	if !slices.Equal(got.Compare, []string{"resolution", "video_codec", "source", "score"}) {
		t.Errorf("compare = %v, want the target's dimensions first", got.Compare)
	}
	if first := got.Tiers["resolution"][0]; !slices.Equal(first, []string{"1080p"}) || !slices.Equal(got.Tiers["resolution"][2], []string{"1080i"}) {
		t.Errorf("resolution tiers = %v, want 1080p alone first and the rest after in their order", got.Tiers["resolution"])
	}
	if !slices.Equal(got.Tiers["video_codec"][0], []string{"h265"}) {
		t.Errorf("video codec tiers = %v", got.Tiers["video_codec"])
	}
	if len(rules.Tiers["resolution"]) != 3 || rules.Compare[0] != "resolution" {
		t.Error("the profile's own rules were changed")
	}
	if same := judge.WithTarget(rules, settings.QualityTarget{AudioCodec: "aac", Container: "m4a"}); !slices.Equal(same.Compare, rules.Compare) {
		t.Errorf("an audio target changed the comparison: %v", same.Compare)
	}
}
