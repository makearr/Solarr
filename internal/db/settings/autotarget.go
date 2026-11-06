package settings

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// AutoTargetLabel tags a target worked out from a profile rather than stored.
const AutoTargetLabel = "Automatic"

// profileShape is the part of a quality profile's rules an automatic target reads: what it ranks first and the
// number it aims for. It is read here, not through identify/score, which settings may not import.
type profileShape struct {
	Require struct {
		Resolution *struct{ Min, Max string } `json:"resolution"`
	} `json:"require"`
	Tiers   map[string][][]string `json:"tiers"`
	Targets map[string][]struct {
		When            map[string]string `json:"when"`
		Min, Ideal, Max string
	} `json:"targets"`
}

// AutoTarget is the target a library aims for when it asks for none of its own: the average of its first quality
// profile, taking what the profile ranks first for each kind of thing (resolution, codec, channels, file type) and the
// middle of each number's range (its ideal, else the mean of its minimum and maximum). ok is false when the profile
// says nothing a target could aim for.
func AutoTarget(profile QualityProfile, mediaType string) (target QualityTarget, ok bool) {
	var shape profileShape
	if json.Unmarshal([]byte(profile.RulesJSON), &shape) != nil {
		return QualityTarget{}, false
	}
	first := func(dimension string) string {
		if groups := shape.Tiers[dimension]; len(groups) > 0 && len(groups[0]) > 0 {
			return groups[0][0]
		}
		return ""
	}
	target = QualityTarget{
		Name: AutoTargetLabel + ": " + profile.Name, MediaType: mediaType, Label: AutoTargetLabel,
		Description: "The average of the library's first quality profile, " + profile.Name + ".",
	}
	switch {
	case AudioMediaTypes[mediaType]:
		target.AudioCodec = first("audio_codec")
		target.Container = firstOf(first("extension"), fileTypeOf(first("container")), defaultFileType[target.AudioCodec])
		target.AudioChannels = first("audio_channels")
		target.TolerancePct = 15
		if !losslessCodecs[target.AudioCodec] {
			target.BitrateKbps = rateKbps(shape, "bitrate", map[string]string{"audio_codec": target.AudioCodec})
		}
		target.SampleRateHz = int64(numberOf(shape, "sample_rate", nil))
		target.BitDepth = int64(numberOf(shape, "bit_depth", nil))
		ok = target.AudioCodec != "" || target.SampleRateHz > 0
	case DocumentMediaTypes[mediaType]:
		target.Container = firstOf(first("extension"), fileTypeOf(first("container")))
		ok = target.Container != ""
	default:
		target.Resolution = first("resolution")
		if target.Resolution == "" && shape.Require.Resolution != nil {
			target.Resolution = shape.Require.Resolution.Max
		}
		target.VideoCodec, target.HDR, target.AudioChannels = first("video_codec"), first("hdr"), first("audio_channels")
		target.AudioCodec, target.Container = first("audio_codec"), fileTypeOf(first("container"))
		target.VideoBitrateKbps = rateKbps(shape, "video_bitrate", nil)
		target.AudioBitrateKbps = rateKbps(shape, "audio_bitrate", nil)
		target.FrameRate = numberOf(shape, "frame_rate", nil)
		ok = target.Resolution != "" || target.VideoCodec != "" || target.VideoBitrateKbps != nil
	}
	return target, ok
}

var losslessCodecs = map[string]bool{"flac": true, "alac": true, "wav": true, "ape": true}

// defaultFileType is the file type an audio codec usually lives in, for a profile that ranks codecs only.
var defaultFileType = map[string]string{"aac": "m4a", "alac": "m4a", "mp3": "mp3", "flac": "flac", "opus": "opus", "vorbis": "ogg", "wav": "wav"}

// fileTypeOf is the file extension a container format stands for; empty when it is not one a target names.
func fileTypeOf(container string) string {
	switch strings.ToLower(container) {
	case "matroska", "mkv":
		return "mkv"
	case "mp4", "mp3", "flac", "ogg", "wav", "avi":
		return strings.ToLower(container)
	}
	return ""
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var numberExpression = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(khz|hz|kbps|mbps|bps)?\s*$`)

// numberOf is the middle of the range of one dimension's target (the first whose when matches): its ideal, else the
// mean of its minimum and maximum, in the unit the dimension is stated in (Hz, kbit/s, frames a second, bits).
func numberOf(shape profileShape, dimension string, when map[string]string) float64 {
	for _, target := range shape.Targets[dimension] {
		if len(target.When) > 0 && !whenMatches(target.When, when) {
			continue
		}
		if value := rangeMiddle(target.Min, target.Ideal, target.Max); value > 0 {
			return value
		}
	}
	return 0
}

func rateKbps(shape profileShape, dimension string, when map[string]string) *int64 {
	if value := int64(numberOf(shape, dimension, when)); value > 0 {
		return &value
	}
	return nil
}

func whenMatches(condition, have map[string]string) bool {
	for key, want := range condition {
		if !strings.EqualFold(have[key], want) {
			return false
		}
	}
	return true
}

func rangeMiddle(minimum, ideal, maximum string) float64 {
	if value, ok := parseNumber(ideal); ok {
		return value
	}
	low, lowOK := parseNumber(minimum)
	high, highOK := parseNumber(maximum)
	switch {
	case lowOK && highOK:
		return (low + high) / 2
	case lowOK:
		return low
	case highOK:
		return high
	}
	return 0
}

// parseNumber reads "256kbps" as 256, "1.5Mbps" as 1500 and "48kHz" as 48000: kbit/s for rates, Hz for sample rates.
func parseNumber(text string) (float64, bool) {
	match := numberExpression.FindStringSubmatch(text)
	if match == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(match[2]) {
	case "khz":
		value *= 1000
	case "mbps":
		value *= 1000
	case "bps":
		value /= 1000
	}
	return value, true
}
