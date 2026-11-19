package score

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Problem is one mistake in a profile document: the JSON path of the field, what was written, what would be accepted.
type Problem struct {
	Path        string
	Value       string
	Expectation string
}

// stringDimensions and numberDimensions list the names Facts.stringField and Facts.numberField resolve.
var (
	stringDimensions = []string{
		"title", "protocol", "release_group", "resolution", "source", "video_codec", "hdr", "audio_codec",
		"audio_channels", "scan_type", "container", "extension",
	}
	numberDimensions = []string{
		"seeders", "leechers", "bit_depth", "sample_rate", "frame_rate", "video_bitrate", "audio_bitrate", "overall_bitrate",
	}
)

// targetDimension reports whether a target block may be keyed on name; "bitrate" stands for the overall bitrate.
func targetDimension(name string) bool {
	return name == "bitrate" || slices.Contains(numberDimensions, name)
}

// ValidateStrict lists every mistake Parse tolerates so that stored profiles keep loading: unknown keys, target values
// that do not parse, impossible ranges and weights, compare entries that name nothing, and regexes that do not compile.
func ValidateStrict(data []byte) []Problem {
	var problems []Problem
	add := func(path, value, expectation string) {
		problems = append(problems, Problem{Path: path, Value: value, Expectation: expectation})
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var strict Profile
	if err := decoder.Decode(&strict); err != nil {
		if field, found := strings.CutPrefix(err.Error(), "json: unknown field "); found {
			add("rules", strings.Trim(field, `"`), "a known key (see the profile reference); this one would be ignored")
		}
	}

	profile, err := Parse(data)
	if err != nil {
		return problems
	}

	for index, codec := range profile.Require.RejectCodecs {
		if codec != strings.ToLower(codec) {
			add(fmt.Sprintf("require.reject_codecs[%d]", index), codec, "lower case, as the probe reports codecs")
		}
	}
	for dimension := range profile.Tiers {
		if !slices.Contains(stringDimensions, dimension) {
			add("tiers."+dimension, dimension, "a field a release or file reports: "+strings.Join(stringDimensions, ", "))
		}
	}
	for dimension, targets := range profile.Targets {
		if !targetDimension(dimension) {
			add("targets."+dimension, dimension, "a numeric field: bitrate, "+strings.Join(numberDimensions, ", "))
			continue
		}
		for index, target := range targets {
			problems = append(problems, validateTarget(fmt.Sprintf("targets.%s[%d]", dimension, index), dimension, target)...)
		}
	}
	for index, entry := range profile.Compare {
		if !compareEntryKnown(entry, profile) {
			add(fmt.Sprintf("compare[%d]", index), entry,
				"score, lossless, a field name, a tiers dimension or <number field>_distance")
		}
	}
	for ruleIndex, rule := range profile.Scoring {
		if rule.Score < -1000 || rule.Score > 1000 || math.IsNaN(rule.Score) {
			add(fmt.Sprintf("scoring[%d].score", ruleIndex), fmt.Sprint(rule.Score), "a score between -1000 and 1000")
		}
		for specIndex, spec := range rule.Specs {
			if spec.Matches != "" && CompiledPattern(spec.Matches) == nil {
				add(fmt.Sprintf("scoring[%d].specs[%d].matches", ruleIndex, specIndex), spec.Matches, "a valid regular expression")
			}
		}
	}
	return problems
}

func compareEntryKnown(entry string, profile Profile) bool {
	if entry == "score" || entry == "lossless" {
		return true
	}
	if _, declared := profile.Tiers[entry]; declared {
		return true
	}
	if base, isDistance := strings.CutSuffix(entry, "_distance"); isDistance {
		return targetDimension(base)
	}
	return slices.Contains(stringDimensions, entry) || slices.Contains(numberDimensions, entry)
}

func validateTarget(path, dimension string, target Target) []Problem {
	var problems []Problem
	add := func(field, value, expectation string) {
		problems = append(problems, Problem{Path: path + "." + field, Value: value, Expectation: expectation})
	}
	parsed := map[string]float64{}
	for _, field := range []struct{ name, raw string }{{"min", target.Min}, {"ideal", target.Ideal}, {"max", target.Max}} {
		if field.raw == "" {
			if field.name == "ideal" {
				add("ideal", "", "a value with its unit, such as 320kbps")
			}
			continue
		}
		value, err := parseTargetValue(dimension, field.raw)
		if err != nil {
			add(field.name, field.raw, targetValueHint(dimension))
			continue
		}
		parsed[field.name] = value
	}
	if floor, ok := parsed["min"]; ok {
		if ideal, ok := parsed["ideal"]; ok && floor > ideal {
			add("min", target.Min, "not above ideal")
		}
	}
	if ceiling, ok := parsed["max"]; ok {
		if ideal, ok := parsed["ideal"]; ok && ceiling < ideal {
			add("max", target.Max, "not below ideal")
		}
	}
	if target.Lenience < 0 || target.Lenience >= 1 || math.IsNaN(target.Lenience) {
		add("lenience", fmt.Sprint(target.Lenience), "a share from 0 up to, not including, 1")
	}
	if target.Weights.Over < 0 {
		add("weights.over", fmt.Sprint(target.Weights.Over), "zero or more")
	}
	if target.Weights.Under < 0 {
		add("weights.under", fmt.Sprint(target.Weights.Under), "zero or more")
	}
	for field := range target.When {
		if !slices.Contains(stringDimensions, field) {
			add("when."+field, field, "a field a file reports: "+strings.Join(stringDimensions, ", "))
		}
	}
	return problems
}

func targetValueHint(dimension string) string {
	switch dimension {
	case "bitrate", "video_bitrate", "audio_bitrate", "overall_bitrate":
		return "a rate with its unit: 320kbps, 4Mbps, 40MB/min"
	case "sample_rate":
		return "a rate such as 48kHz or 44100"
	default:
		return "a plain number"
	}
}
