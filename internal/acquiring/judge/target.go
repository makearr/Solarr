package judge

import (
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
)

// WithTarget makes a video library's target the first thing releases are ranked by: the resolution, video codec and
// HDR the target names each become the best tier of their dimension, and those dimensions lead the comparison. The
// profile's own gates stay: a target never lets through what the profile refuses.
func WithTarget(rules score.Profile, target settings.QualityTarget) score.Profile {
	named := []struct{ dimension, value string }{
		{"resolution", target.Resolution}, {"video_codec", target.VideoCodec}, {"hdr", target.HDR},
	}
	tiers := make(map[string][][]string, len(rules.Tiers)+len(named))
	for dimension, groups := range rules.Tiers {
		tiers[dimension] = groups
	}
	var leading []string
	for _, entry := range named {
		if entry.value == "" || strings.EqualFold(entry.value, "sdr") {
			continue
		}
		groups := [][]string{{entry.value}}
		for _, group := range rules.Tiers[entry.dimension] {
			rest := slices.DeleteFunc(slices.Clone(group), func(value string) bool { return strings.EqualFold(value, entry.value) })
			if len(rest) > 0 {
				groups = append(groups, rest)
			}
		}
		tiers[entry.dimension] = groups
		leading = append(leading, entry.dimension)
	}
	if len(leading) == 0 {
		return rules
	}
	compare := slices.Clone(leading)
	for _, dimension := range rules.Compare {
		if !slices.Contains(leading, dimension) {
			compare = append(compare, dimension)
		}
	}
	rules.Tiers, rules.Compare = tiers, compare
	return rules
}
