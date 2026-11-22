package library

import (
	"encoding/json"
	"strconv"
	"strings"
)

// bitrateTolerance and sampleRateTolerance are fractional because encoder rounding (320 claimed, 315000 bps measured) is not a mislabel; ADR-39 names no value, so these are adjustable starting points.
const (
	bitrateTolerance    = 0.10
	sampleRateTolerance = 0.01
)

// AttributeMismatch is one attribute where the claim and the measurement
// disagree beyond tolerance -- ADR-39: "a claim that fails its probe is a
// signal, not just a correction."
type AttributeMismatch struct {
	Claimed  string `json:"claimed"`
	Measured string `json:"measured"`
}

// CompareAttributes finds claimed fields that disagree with measurement beyond tolerance; only fixed-value fields compare, and a field neither side claimed is missing information, not a disagreement.
func CompareAttributes(claimed, measured AttributeClaim) map[string]AttributeMismatch {
	mismatches := map[string]AttributeMismatch{}

	if claimed.Format() != "" && measured.AudioCodec != "" &&
		!strings.EqualFold(claimed.Format(), measured.AudioCodec) {
		mismatches["audio_codec"] = AttributeMismatch{Claimed: claimed.Format(), Measured: measured.AudioCodec}
	}
	if measured.AudioCodec != "" && claimed.Lossless != measured.Lossless {
		mismatches["lossless"] = AttributeMismatch{
			Claimed: strconv.FormatBool(claimed.Lossless), Measured: strconv.FormatBool(measured.Lossless),
		}
	}
	if claimed.BitDepth > 0 && measured.BitDepth > 0 && claimed.BitDepth != measured.BitDepth {
		mismatches["bit_depth"] = AttributeMismatch{Claimed: strconv.Itoa(claimed.BitDepth), Measured: strconv.Itoa(measured.BitDepth)}
	}
	if claimed.SampleRate > 0 && measured.SampleRate > 0 &&
		relativeDelta(claimed.SampleRate, measured.SampleRate) > sampleRateTolerance {
		mismatches["sample_rate"] = AttributeMismatch{Claimed: strconv.Itoa(claimed.SampleRate), Measured: strconv.Itoa(measured.SampleRate)}
	}
	if claimed.BitrateKbps > 0 && measured.BitrateKbps > 0 &&
		relativeDelta(claimed.BitrateKbps, measured.BitrateKbps) > bitrateTolerance {
		mismatches["bitrate_kbps"] = AttributeMismatch{Claimed: strconv.Itoa(claimed.BitrateKbps), Measured: strconv.Itoa(measured.BitrateKbps)}
	}
	return mismatches
}

// MismatchJSON encodes the result for media_object.attribute_mismatch_json: empty for no mismatch, never "{}", so NULL still means never compared.
func MismatchJSON(mismatches map[string]AttributeMismatch) string {
	if len(mismatches) == 0 {
		return ""
	}
	encoded, err := json.Marshal(mismatches)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// Format is AttributeClaim's own codec-ish field for comparison purposes:
// AudioCodec when a caller populated it (the measured side always does),
// falling back to whatever the claimed side calls its codec/container name.
func (c AttributeClaim) Format() string {
	if c.AudioCodec != "" {
		return c.AudioCodec
	}
	return c.Container
}

func relativeDelta(claimed, measured int) float64 {
	if claimed == 0 {
		return 0
	}
	delta := claimed - measured
	if delta < 0 {
		delta = -delta
	}
	return float64(delta) / float64(claimed)
}
