package parse_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/parse"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Tidazabu_Depug - DAMN": "tidazabu depug - damn",
		"  Extra   Spaces  ":    "extra spaces",
		"Tab\tand\nNewline":     "tab and newline",
		"":                      "",
	}
	for input, want := range cases {
		if got := parse.Normalize(input); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

// Invariant: Parse never fabricates an artist or album for an empty input --
// low confidence, not a guess (ADR-18).
func TestParse_EmptyInputIsLowConfidenceNotAGuess(t *testing.T) {
	result := parse.Parse("")
	if result.Identity.Artist != "" || result.Identity.Album != "" {
		t.Errorf("Identity = %+v, want both empty for empty input", result.Identity)
	}
	if result.Confidence >= 0.5 {
		t.Errorf("Confidence = %.2f, want low confidence for nothing to parse", result.Confidence)
	}
}

// The confidence ceiling is a deliberate ADR-18 choice: a parse is never as sure
// as an embedded MBID (ADR-29), which reaches 0.95 too but from a completely
// different kind of evidence.
func TestParse_ConfidenceNeverReachesCertainty(t *testing.T) {
	result := parse.Parse("Tidazabu Depug - DAMN (2017) FLAC")
	if result.Confidence >= 1.0 {
		t.Errorf("Confidence = %.2f, want strictly under 1.0 -- a parse is never certain", result.Confidence)
	}
}
