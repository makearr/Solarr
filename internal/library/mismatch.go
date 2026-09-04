package library

import (
	"encoding/json"
	"strconv"
	"strings"
)

// bitrateTolerance and sampleRateTolerance are fractional, not absolute: a
// claimed "320" against a measured 315000 bps is real-world encoder rounding,
// not a mislabelled release. ADR-39 asks for "a tolerance, not equality"
// without naming one; these are a deliberate, adjustable starting point, not
// a value derived from any external source.
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

// CompareAttributes finds every claimed field that disagrees with what was
// measured, beyond the tolerances above. Only fields with fixed intended
// values are compared (a codec claim, a lossless claim, a stated bitrate or
// sample rate) -- a field neither side claimed is not a disagreement, just
// missing information.
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

// MismatchJSON encodes CompareAttributes' result the way
// media_object.attribute_mismatch_json expects it -- empty for no mismatch,
// never the literal string "{}", so a caller can tell "compared, agreed" from
// "never compared" by whether the column is NULL.
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
