package parse_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/parse"
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
