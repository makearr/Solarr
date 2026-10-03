package judge

import (
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
)

// targetLanguageScore is heard as a tie-breaker between releases of equal quality, as the language preferences are.
const targetLanguageScore = 5

func kbps(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10) + "kbps"
}

// WithTarget makes a video library's target the first thing releases are ranked by: the resolution, video codec and
// HDR the target names each become the best tier of their dimension, and those dimensions lead the comparison. The
// profile's own gates stay: a target never lets through what the profile refuses.
func WithTarget(rules score.Profile, target settings.QualityTarget) score.Profile {
	named := []struct{ dimension, value string }{
		{"resolution", target.Resolution}, {"video_codec", target.VideoCodec}, {"hdr", target.HDR}, {"audio_channels", target.AudioChannels},
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
	targets := make(map[string][]score.Target, len(rules.Targets)+3)
	for dimension, list := range rules.Targets {
		targets[dimension] = list
	}
	// The measured numbers rank by their distance from the ideal, the nearest first; a dimension the profile already
	// ranks as tiers is left to it.
	for _, entry := range []struct {
		dimension, ideal string
		set              bool
	}{
		{"video_bitrate", kbps(target.VideoBitrateKbps), target.VideoBitrateKbps != nil},
		{"audio_bitrate", kbps(target.AudioBitrateKbps), target.AudioBitrateKbps != nil},
		{"frame_rate", strconv.FormatFloat(target.FrameRate, 'f', -1, 64), target.FrameRate > 0},
	} {
		if !entry.set {
			continue
		}
		if _, tiered := rules.Tiers[entry.dimension]; tiered {
			continue
		}
		targets[entry.dimension] = []score.Target{{Ideal: entry.ideal, Weights: score.TargetWeights{Over: 1, Under: 1}}}
		leading = append(leading, entry.dimension+"_distance")
	}
	scoring := slices.Clone(rules.Scoring)
	for _, language := range target.AudioLanguages {
		scoring = append(scoring, score.ScoringRule{Name: "Target audio language: " + language, Score: targetLanguageScore, Specs: []score.Spec{{Field: "audio_languages", Has: language}}})
	}
	for _, language := range target.SubtitleLanguages {
		scoring = append(scoring, score.ScoringRule{Name: "Target subtitle language: " + language, Score: targetLanguageScore, Specs: []score.Spec{{Field: "subtitle_languages", Has: language}}})
	}
	rules.Scoring, rules.Targets = scoring, targets
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
