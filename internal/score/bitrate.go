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

// matchTarget returns the first BitrateTarget whose When conditions all match the
// file's own fields -- declaration order is significant, first match wins.
func matchTarget(targets []BitrateTarget, file FileFacts) (BitrateTarget, bool) {
	facts := Facts{File: file}
	for _, target := range targets {
		if targetMatches(target.When, facts) {
			return target, true
		}
	}
	return BitrateTarget{}, false
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

// bitrateDistance is |log2(rate/ideal)|, scaled by the over/under weight (ADR-40).
// ok is false when there is nothing to measure against: no target, ideal, or probe.
func bitrateDistance(overallBitsPerSecond int64, target BitrateTarget, weights BitrateWeights, hasTarget bool) (distance float64, ok bool) {
	if !hasTarget || overallBitsPerSecond <= 0 {
		return 0, false
	}
	ideal, err := parseBitRate(target.Bitrate.Ideal)
	if err != nil || ideal <= 0 {
		return 0, false
	}
	rate := float64(overallBitsPerSecond)
	raw := math.Abs(math.Log2(rate / ideal))
	weight := weights.Under
	if rate > ideal {
		weight = weights.Over
	}
	return raw * weight, true
}
