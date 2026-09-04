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

// Regression, found against a real release name during this project's own live
// verification pass: an edition marker like "EP" must only ever match as its own
// word. A raw substring check matched "ep" inside "Mesofisak" and classified the
// whole album title as edition noise, stripping it to nothing.
func TestParse_EditionWordsMatchWholeWordsOnly(t *testing.T) {
	result := parse.Parse("Cibiz M-Zuroveguv Peral to Lopus Like Mesofisak Tapas-12INCH VINYL-FLAC-1989-LoKET")
	if result.Identity.Album != "Zuroveguv Peral to Lopus Like Mesofisak Tapas" {
		t.Errorf("Album = %q, want the full title kept -- \"ep\" inside \"Mesofisak\" must not"+
			" be read as an EP marker", result.Identity.Album)
	}
}

// A bare bit-depth or sample-rate token ("24BIT", "96KHZ") is common in real
// WEB/FLAC scene names and, before this, leaked straight into the album title.
func TestParse_BitDepthAndSampleRateStripFromTheAlbumTitle(t *testing.T) {
	result := parse.Parse("The Zegabab-Muvohem And Zetave-24BIT-96KHZ-WEB-FLAC-2025-OBZEN")
	if result.Identity.Album != "Muvohem And Zetave" {
		t.Errorf("Album = %q, want bit depth and sample rate stripped", result.Identity.Album)
	}
}

// Regression, found against a real release name during this project's own live
// verification pass: three or more consecutive noise brackets collapsing to bare
// spaces ("-[16BIT]-[WEBFLAC]-[2005]-") can accidentally create a
// whitespace-dash-whitespace sequence that the human-readable " - " artist/album
// separator matches -- which is why "webflac" as a single recognised token was
// deliberately NOT added despite being a real, common scene convention: it turned
// this exact input into a completely different (and wrong) artist/album split.
// This test pins the safe, if slightly less clean, behaviour that shipped instead.
func TestParse_ConsecutiveNoiseBracketsDoNotCorruptTheArtistAlbumSplit(t *testing.T) {
	result := parse.Parse("The Somimema-Gold-[16BIT]-[WEBFLAC]-[2005]-TDS")
	if result.Identity.Artist != "The Somimema" {
		t.Errorf("Artist = %q, want \"The Somimema\" -- consecutive noise brackets must not shift the split",
			result.Identity.Artist)
	}
	if result.Identity.Year != 2005 {
		t.Errorf("Year = %d, want 2005", result.Identity.Year)
	}
}
