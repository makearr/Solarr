package parse_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/identify/parse"
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

// Regression: a bracket whose own contents contain a dash ("[WEB-AAC]")
// leaves a run of adjacent dashes behind once extractBrackets replaces it with
// a space -- the same "accidental ' - ' separator" failure mode as the
// WEBFLAC case above, found this time against a real download-folder name.
func TestParse_DashInsideARemovedBracketDoesNotCorruptTheSplit(t *testing.T) {
	result := parse.Parse("Mupaf-Give Me Love-[WEB-AAC]-[1997]-TDS")
	want := parse.Identity{Artist: "Mupaf", Album: "Give Me Love", Year: 1997}
	if result.Identity != want {
		t.Errorf("Identity = %+v, want %+v", result.Identity, want)
	}
	if result.Attributes.ReleaseGroup != "TDS" {
		t.Errorf("ReleaseGroup = %q, want \"TDS\"", result.Attributes.ReleaseGroup)
	}
}

// Regression: an EAN/UPC-barcoded WEB release joins its tag run onto the
// title with a plain dash even though the artist itself was already split off
// by a spaced " - ", so the album segment never gets a second chance to
// split -- findUnspacedTagRun exists for exactly this shape.
func TestParse_EANBarcodedWebReleaseSplitsTheTagRun(t *testing.T) {
	result := parse.Parse("Pedaf Licoli and Susogi feat Ticumos Love - Never Dulaz Nupi Vehupip-5021732472793-WEB-2024-ZzZz")
	want := parse.Identity{Artist: "Pedaf Licoli and Susogi feat Ticumos Love", Album: "Never Dulaz Nupi Vehupip", Year: 2024}
	if result.Identity != want {
		t.Errorf("Identity = %+v, want %+v", result.Identity, want)
	}
	if result.Attributes.Source != "web" || result.Attributes.ReleaseGroup != "ZzZz" {
		t.Errorf("Source = %q, ReleaseGroup = %q, want \"web\", \"ZzZz\"",
			result.Attributes.Source, result.Attributes.ReleaseGroup)
	}
}

// Regression: a hyphenated artist name must never feed its own dash into
// release-group extraction -- "Jay-Z" has no scene group here at all, but
// ReleaseGroupRegex's dash-token scan would otherwise read "-Z" as one.
func TestParse_HyphenatedArtistNameIsNotMistakenForAReleaseGroup(t *testing.T) {
	result := parse.Parse("Jay-Z - 4:44 (Deluxe Edition) (2017) 320")
	if result.Attributes.ReleaseGroup != "" {
		t.Errorf("ReleaseGroup = %q, want none -- \"Jay-Z\" is the artist, not scene-group evidence",
			result.Attributes.ReleaseGroup)
	}
	if result.Identity.Artist != "Jay-Z" {
		t.Errorf("Artist = %q, want \"Jay-Z\"", result.Identity.Artist)
	}
}

// Regression: a live recording's own embedded date must not make the whole
// segment noise -- a whole-segment any-word-matches rule used to discard
// "Live At Begonobavuni Setesevug Hikufusog 20 1968" in its entirety for one
// recognised year. The trailing year is still consumed as a claim (the
// existing, order-independent "the smaller of two years found is the
// original/primary one, the larger is the end date" rule -- already exercised
// by the Gemu Codub Sa. golden-corpus case -- applies here too: 1968 is the
// concert date, 2025 the WEB release date), but the rest of the descriptive
// title must survive.
func TestParse_EmbeddedYearInADescriptivePhraseKeepsTheRestOfTheTitle(t *testing.T) {
	result := parse.Parse("The Vutis-Live At Begonobavuni Setesevug Hikufusog 20 1968-16BIT-WEB-FLAC-2025-OBZEN")
	if result.Identity.Album != "Live At Begonobavuni Setesevug Hikufusog 20" {
		t.Errorf("Album = %q, want the rest of the descriptive title kept", result.Identity.Album)
	}
	if result.Identity.Year != 1968 || result.Identity.YearEnd != 2025 {
		t.Errorf("Year/YearEnd = %d/%d, want 1968/2025", result.Identity.Year, result.Identity.YearEnd)
	}
}

// Regression: "1969-1982" is a year range, not a title followed by a scene
// tag run -- findUnspacedTagRun must not split it apart before
// yearRangeRe ever sees the pair whole.
func TestParse_YearRangeIsNotMistakenForATagRun(t *testing.T) {
	result := parse.Parse("Zos Mefarada - Studio Discography 1969-1982 (10 albums)(flac)")
	if result.Identity.Year != 1969 || result.Identity.YearEnd != 1982 {
		t.Errorf("Year/YearEnd = %d/%d, want 1969/1982", result.Identity.Year, result.Identity.YearEnd)
	}
	if !result.Identity.Discography {
		t.Error("Discography = false, want true")
	}
}

// Regression: a multi-word scene tag ("ENRiCH iNT", where "iNT" marks an
// internal/scene-only release) must be recognised by its first word matching
// the already-extracted release group, not by exact equality against the
// whole segment.
func TestParse_ReleaseGroupWithAQualifierSuffixIsStillRecognised(t *testing.T) {
	result := parse.Parse("Cin Mucu-These Days-WEB-1995-ENRiCH iNT")
	if result.Identity.Album != "These Days" {
		t.Errorf("Album = %q, want \"These Days\"", result.Identity.Album)
	}
	if result.Attributes.ReleaseGroup != "ENRiCH" {
		t.Errorf("ReleaseGroup = %q, want \"ENRiCH\"", result.Attributes.ReleaseGroup)
	}
}

// Regression: a real scene group tag in a trailing bracket ("[PMEDIA]") must
// not also survive, redundantly, as literal bracket text in the album title
// once it has been correctly extracted as the release group.
func TestParse_ReleaseGroupBracketDoesNotAlsoSurviveInTheAlbumTitle(t *testing.T) {
	result := parse.Parse("Raferasom-Misulab Zita 1975 2024 FLAC [PMEDIA]")
	if result.Identity.Album != "Misulab Zita" {
		t.Errorf("Album = %q, want \"Misulab Zita\" -- \"[PMEDIA]\" is the release group, not title text",
			result.Identity.Album)
	}
	if result.Attributes.ReleaseGroup != "PMEDIA" {
		t.Errorf("ReleaseGroup = %q, want \"PMEDIA\"", result.Attributes.ReleaseGroup)
	}
}

// Regression: "VLS" (Vinyl Single) and "CDM" (CD Maxi) are common scene
// source abbreviations that must resolve to the same canonical source a
// spelled-out release would use, not leak into the album title as unrecognised
// text.
func TestParse_SceneSourceAbbreviationsResolveToCanonicalSource(t *testing.T) {
	cases := map[string]string{
		"KA Voho L-Dulaz Kata To Mapu-VLS-FLAC-1988-THEVOiD": "vinyl",
		"Lamig Vu Linumudo-Cedak Tivilute-CDM-FLAC-1996-CHS": "cd",
	}
	for input, wantSource := range cases {
		result := parse.Parse(input)
		if result.Attributes.Source != wantSource {
			t.Errorf("Parse(%q).Attributes.Source = %q, want %q", input, result.Attributes.Source, wantSource)
		}
		if strings.Contains(result.Identity.Album, "VLS") || strings.Contains(result.Identity.Album, "CDM") {
			t.Errorf("Parse(%q).Identity.Album = %q, want the source abbreviation stripped", input, result.Identity.Album)
		}
	}
}
