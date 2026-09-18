package parse_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/parse"
)

// TestParse_HashedReleaseIsRejected ports Lidarr's ValidateBeforeParsing: a
// bare hash or known junk filename carries no real identity and must never be
// guessed at.
func TestParse_HashedReleaseIsRejected(t *testing.T) {
	cases := []string{
		"a1b2c3d4e5f60718293a4b5c6d7e8f90",
		"abc",
		"123",
	}
	for _, input := range cases {
		result := parse.Parse(input)
		if result.Confidence != 0 {
			t.Errorf("Parse(%q).Confidence = %.2f, want 0 for a rejected hashed release", input, result.Confidence)
		}
		if result.Identity.Artist != "" || result.Identity.Album != "" {
			t.Errorf("Parse(%q).Identity = %+v, want both empty", input, result.Identity)
		}
	}
}

// TestParse_ProperAndRepackAreClaimed ports QualityParser's ProperRegex and
// RepackRegex into model.Attributes.ProperRepack -- the release-name claim
// side of ADR-39, mirroring media_attributes.proper_repack's measured side.
func TestParse_ProperAndRepackAreClaimed(t *testing.T) {
	cases := map[string]bool{
		"Artist-Album-PROPER-CD-FLAC-2020-GROUP": true,
		"Artist-Album-REPACK-CD-FLAC-2020-GROUP": true,
		"Artist-Album-REAL-CD-FLAC-2020-GROUP":   true,
		"Artist-Album-CD-FLAC-2020-GROUP":        false,
	}
	for input, want := range cases {
		if got := parse.Parse(input).Attributes.ProperRepack; got != want {
			t.Errorf("Parse(%q).Attributes.ProperRepack = %v, want %v", input, got, want)
		}
	}
}

func TestCleanAlbumTitle(t *testing.T) {
	got := parse.CleanAlbumTitle("Vupuh of Vesetekepi (Deluxe Edition)")
	if got != "Vupuh of Vesetekepi" {
		t.Errorf("CleanAlbumTitle = %q, want \"Vupuh of Vesetekepi\"", got)
	}
}

func TestCleanTrackTitle(t *testing.T) {
	got := parse.CleanTrackTitle("Without Me (Produced By Birino & Jeff Bass)")
	if got != "Without Me" {
		t.Errorf("CleanTrackTitle = %q, want \"Without Me\"", got)
	}
}

func TestRemoveBracketsAndContents(t *testing.T) {
	got := parse.RemoveBracketsAndContents("Dagu Vo Hivu [EAC-FLAC]")
	if got != "Dagu Vo Hivu" {
		t.Errorf("RemoveBracketsAndContents = %q, want \"Dagu Vo Hivu\"", got)
	}
}

func TestRemoveAfterDash(t *testing.T) {
	got := parse.RemoveAfterDash("DAMN - Deluxe")
	if got != "DAMN" {
		t.Errorf("RemoveAfterDash = %q, want \"DAMN\"", got)
	}
}

func TestNormalizeTitle(t *testing.T) {
	got := parse.NormalizeTitle("The Very Best Of, The Cimuhon Tozali!")
	if got != "very best rolling stones" {
		t.Errorf("NormalizeTitle = %q, want \"very best rolling stones\"", got)
	}
}

// TestCleanArtistName pins the two-pass rewrite of NormalizeRegex's lookaround
// (ATTRIBUTION.md): a filler word in the middle drops, but one that is the
// entire name does not -- the original's (?<!^)/(?!$) guard.
func TestCleanArtistName(t *testing.T) {
	// "the" leading the whole name sits at the string's own edge, which the
	// original's (?<!^)/(?!$) guard excludes from stripping -- only a filler
	// word strictly inside the name is dropped.
	if got := parse.CleanArtistName("The Cimuhon Tozali"); got != "therollingstones" {
		t.Errorf("CleanArtistName(%q) = %q, want %q", "The Cimuhon Tozali", got, "therollingstones")
	}
	if got := parse.CleanArtistName("Rarul the Vamofu"); got != "underbridge" {
		t.Errorf("CleanArtistName(%q) = %q, want %q -- a filler word in the middle is stripped", "Rarul the Vamofu", got, "underbridge")
	}
	if got := parse.CleanArtistName("The The"); got != "thethe" {
		t.Errorf("CleanArtistName(%q) = %q, want %q -- a filler word at either edge is never stripped", "The The", got, "thethe")
	}
}

// TestParse_SampleRateIsClaimed ports todo 4.2: a bare sample-rate token
// ("96KHZ", "44.1KHZ") is now recorded on model.Attributes.SampleRate in Hz,
// not just stripped from the album title.
func TestParse_SampleRateIsClaimed(t *testing.T) {
	cases := map[string]int{
		"The Zegabab-Muvohem And Zetave-24BIT-96KHZ-WEB-FLAC-2025-OBZEN":     96000,
		"Gohudo Cosicatebi-Citus On Vokeva-24BIT-192KHZ-WEB-FLAC-2003-TiMES": 192000,
	}
	for input, want := range cases {
		if got := parse.Parse(input).Attributes.SampleRate; got != want {
			t.Errorf("Parse(%q).Attributes.SampleRate = %d, want %d", input, got, want)
		}
	}
}

// TestParse_BitrateModeAndVBRPreset ports todo 4.2's split of the old
// negative-BitrateKbps sentinel into explicit fields: a bare numeric claim
// implies "cbr", a named preset implies "vbr" and records the preset itself.
func TestParse_BitrateModeAndVBRPreset(t *testing.T) {
	result := parse.Parse("A.I. - Zal & Binesu [2007/MP3/V0(VBR)]")
	if result.Attributes.BitrateMode != "vbr" {
		t.Errorf("BitrateMode = %q, want \"vbr\"", result.Attributes.BitrateMode)
	}
	if result.Attributes.VBRPreset != "V0" {
		t.Errorf("VBRPreset = %q, want \"V0\"", result.Attributes.VBRPreset)
	}
	if result.Attributes.BitrateKbps != 0 {
		t.Errorf("BitrateKbps = %d, want 0 -- a VBR preset is not a fixed rate", result.Attributes.BitrateKbps)
	}

	cbr := parse.Parse("Jay-Z - 4:44 (Deluxe Edition) (2017) 320")
	if cbr.Attributes.BitrateMode != "cbr" {
		t.Errorf("BitrateMode = %q, want \"cbr\"", cbr.Attributes.BitrateMode)
	}
	if cbr.Attributes.BitrateKbps != 320 {
		t.Errorf("BitrateKbps = %d, want 320", cbr.Attributes.BitrateKbps)
	}
}
