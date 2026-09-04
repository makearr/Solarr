package score

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var rateExpression = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)\s*(mbps|kbps|bps|mb/min|kb/min)$`)

// parseBitRate converts a human-readable rate to bits/sec -- MB/min and kB/min
// exist because TRaSH-Guides-style definitions express rate as per-minute size.
func parseBitRate(raw string) (float64, error) {
	match := rateExpression.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return 0, fmt.Errorf("score: invalid bitrate %q", raw)
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, fmt.Errorf("score: invalid bitrate %q: %w", raw, err)
	}
	switch strings.ToLower(match[2]) {
	case "mbps":
		return value * 1_000_000, nil
	case "kbps":
		return value * 1_000, nil
	case "bps":
		return value, nil
	case "mb/min":
		return value * 1_000_000 * 8 / 60, nil
	case "kb/min":
		return value * 1_000 * 8 / 60, nil
	default:
		return 0, fmt.Errorf("score: unknown bitrate unit in %q", raw)
	}
}

var sampleRateExpression = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)\s*(khz|hz)$`)

// parseSampleRate converts a human-readable rate ("48kHz", "44100") to Hz, the
// same unit Facts.numberField("sample_rate") reports -- a bare number is
// already Hz, matching how a probe reports it.
func parseSampleRate(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if match := sampleRateExpression.FindStringSubmatch(raw); match != nil {
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return 0, fmt.Errorf("score: invalid sample rate %q: %w", raw, err)
		}
		if strings.EqualFold(match[2], "khz") {
			return value * 1000, nil
		}
		return value, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("score: invalid sample rate %q", raw)
	}
	return value, nil
}

// measuredFieldFor is where a target dimension's own name differs from the
// Facts field it is measured against -- only bitrate does; every other
// dimension (sample_rate, bit_depth, ...) already shares its name with a
// Facts.numberField entry.
func measuredFieldFor(dimension string) string {
	if dimension == "bitrate" {
		return "overall_bitrate"
	}
	return dimension
}

// parseTargetValue converts a Target's min/ideal/max string to the number the
// dimension's own Facts field is measured in -- bitrate and sample_rate accept
// a unit suffix, everything else is a bare number.
func parseTargetValue(dimension, raw string) (float64, error) {
	switch dimension {
	case "bitrate":
		return parseBitRate(raw)
	case "sample_rate":
		return parseSampleRate(raw)
	default:
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return 0, fmt.Errorf("score: invalid %s value %q", dimension, raw)
		}
		return value, nil
	}
}

// matchTarget returns the first Target whose When conditions all match the
// file's own fields -- declaration order is significant, first match wins.
func matchTarget(targets []Target, file FileFacts) (Target, bool) {
	facts := Facts{File: file}
	for _, target := range targets {
		if targetMatches(target.When, facts) {
			return target, true
		}
	}
	return Target{}, false
}

func targetMatches(when map[string]string, facts Facts) bool {
	for field, want := range when {
		got, ok := facts.stringField(field)
		if !ok || !strings.EqualFold(got, want) {
			return false
		}
	}
	return true
}

// targetDistance is |log2(measured/ideal)|, scaled by the over/under weight
// (ADR-40, amended to any numeric dimension). ok is false when there is
// nothing to measure against: no matching target, no ideal, or the dimension
// itself was not probed.
func targetDistance(dimension string, target Target, hasTarget bool, facts Facts) (distance float64, ok bool) {
	if !hasTarget {
		return 0, false
	}
	measured, present := facts.numberField(measuredFieldFor(dimension))
	if !present || measured <= 0 {
		return 0, false
	}
	ideal, err := parseTargetValue(dimension, target.Ideal)
	if err != nil || ideal <= 0 {
		return 0, false
	}
	raw := math.Abs(math.Log2(measured / ideal))
	weight := target.Weights.Under
	if measured > ideal {
		weight = target.Weights.Over
	}
	return raw * weight, true
}

// targetResult is one dimension's resolved target and distance, computed once
// per Evaluate call and shared between the quality vector (every dimension)
// and the bitrate-minimum gate (the "bitrate" dimension specifically).
type targetResult struct {
	target     Target
	hasTarget  bool
	distance   float64
	distanceOK bool
}

// resolveTargets runs matchTarget and targetDistance for every dimension a
// profile declares targets for, once, so Evaluate and the gates it feeds never
// recompute the same match twice.
func resolveTargets(targets map[string][]Target, facts Facts) map[string]targetResult {
	resolved := make(map[string]targetResult, len(targets))
	for dimension, candidates := range targets {
		target, hasTarget := matchTarget(candidates, facts.File)
		distance, ok := targetDistance(dimension, target, hasTarget, facts)
		resolved[dimension] = targetResult{target: target, hasTarget: hasTarget, distance: distance, distanceOK: ok}
	}
	return resolved
}
