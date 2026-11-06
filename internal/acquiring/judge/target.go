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

// overshootWeight makes a number above its ideal cost half what the same number below it does, for a target that prefers more.
const overshootWeight = 0.5

// resolutionLadder is the resolutions in order from the highest, by the position of each in the list.
var resolutionNames = []string{"2160p", "1440p", "1080p", "720p", "576p", "480p"}

var resolutionLadder = func() map[string][]string {
	ladder := make(map[string][]string, len(resolutionNames))
	for _, name := range resolutionNames {
		ladder[name] = resolutionNames
	}
	return ladder
}()

// aboveAndBelow ranks the resolutions around a target: the target, then the ones above it (the nearest first), then the
// ones below it (the nearest first). With IgnoreBelow the ones below are left unranked, which ranks them after all the rest.
func aboveAndBelow(value string, ladder []string, target settings.QualityTarget) [][]string {
	at := slices.IndexFunc(ladder, func(name string) bool { return strings.EqualFold(name, value) })
	groups := [][]string{{value}}
	for index := at - 1; index >= 0; index-- {
		groups = append(groups, []string{ladder[index]})
	}
	if target.IgnoreBelow {
		return groups
	}
	for index := at + 1; index < len(ladder); index++ {
		groups = append(groups, []string{ladder[index]})
	}
	return groups
}

func kbps(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10) + "kbps"
}

// WithTarget makes a library's target the first thing releases are ranked by: what the target names (resolution,
// video codec and HDR for video; the audio codec for an audio library) becomes the best tier of its dimension, and
// those dimensions lead the comparison. The profile's own gates stay: a target never lets through what it refuses.
func WithTarget(rules score.Profile, target settings.QualityTarget) score.Profile {
	named := []struct{ dimension, value string }{
		{"resolution", target.Resolution}, {"video_codec", target.VideoCodec}, {"hdr", target.HDR}, {"audio_channels", target.AudioChannels},
	}
	audio := !target.IsVideo()
	if audio {
		named = append(named, struct{ dimension, value string }{"audio_codec", target.AudioCodec})
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
		if ladder, ranked := resolutionLadder[strings.ToLower(entry.value)]; entry.dimension == "resolution" && ranked && (target.PreferOver || target.IgnoreBelow) {
			groups = aboveAndBelow(entry.value, ladder, target)
		} else {
			for _, group := range rules.Tiers[entry.dimension] {
				rest := slices.DeleteFunc(slices.Clone(group), func(value string) bool { return strings.EqualFold(value, entry.value) })
				if len(rest) > 0 {
					groups = append(groups, rest)
				}
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
		// An audio library's one bitrate is the whole file's, which is also what a release name claims.
		{"overall_bitrate", kbps(target.BitrateKbps), audio && target.BitrateKbps != nil},
		{"sample_rate", strconv.FormatInt(target.SampleRateHz, 10), audio && target.SampleRateHz > 0},
		{"bit_depth", strconv.FormatInt(target.BitDepth, 10), audio && target.BitDepth > 0},
	} {
		if !entry.set {
			continue
		}
		if _, tiered := rules.Tiers[entry.dimension]; tiered {
			continue
		}
		over := 1.0
		if target.PreferOver {
			over = overshootWeight
		}
		targets[entry.dimension] = []score.Target{{Ideal: entry.ideal, Weights: score.TargetWeights{Over: over, Under: 1}}}
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
