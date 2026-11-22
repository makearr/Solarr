package music

import (
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

func TestNeutralCore(t *testing.T) {
	for title, want := range map[string]string{
		"Song":                          "Song",
		"Song (2011 Remaster)":          "Song",
		"Song (Remastered Version)":     "Song",
		"Song - 2011 Remaster":          "Song",
		"Song (feat. Other Name)":       "Song",
		"Song [Explicit]":               "Song",
		"Song (Live)":                   "Song (Live)",
		"Song (Live) (2009 Remaster)":   "Song (Live)",
		"Song - Part 2":                 "Song - Part 2",
		"A - B":                         "A - B",
		"Song (Shep's club mix)":        "Song (Shep's club mix)",
		"Song (Mono) (Album Version)":   "Song",
		"Song (Acoustic Demo":           "Song (Acoustic Demo",
		"(2011 Remaster)":               "",
		"Song (Bonus Track) - Remaster": "Song",
	} {
		if got := neutralCore(title); got != want {
			t.Errorf("neutralCore(%q) = %q, want %q", title, got, want)
		}
	}
}

func tracklist(titles ...string) musicbrainz.Release {
	medium := musicbrainz.Medium{Position: 1}
	for i, title := range titles {
		medium.Tracks = append(medium.Tracks, musicbrainz.Track{Position: i + 1, Title: title})
	}
	return musicbrainz.Release{Media: []musicbrainz.Medium{medium}}
}

// A whole album tagged "(Remastered 2011)" against the plain titles still pairs every file.
func TestFitTracklist_QualifiersOfAPressingDoNotBreakThePairing(t *testing.T) {
	var locals []LocalTrack
	var titles []string
	for i := 1; i <= 10; i++ {
		title := fmt.Sprintf("Invented Song %d", i)
		titles = append(titles, title)
		locals = append(locals, LocalTrack{Track: i, Title: Claim{Value: title + " (Remastered 2011)", Provenance: ProvenanceTag}})
	}
	if fit := fitTracklist(locals, tracklist(titles...), false); fit.supported != 10 {
		t.Errorf("supported = %d, want all 10", fit.supported)
	}
}

// A live track is another recording: its file pairs with it, not with the studio take.
func TestFitTracklist_ALiveQualifierStillTellsRecordingsApart(t *testing.T) {
	locals := []LocalTrack{
		{Track: 2, Title: Claim{Value: "Invented Song (Live)", Provenance: ProvenanceTag}},
	}
	fit := fitTracklist(locals, tracklist("Invented Song", "Invented Song (Live)"), false)
	if fit.tracks[0] == nil || fit.tracks[0].Position != 2 {
		t.Errorf("tracks[0] = %+v, want the live track at position 2", fit.tracks[0])
	}
}

func releaseWithPregap(titles ...string) musicbrainz.Release {
	release := tracklist(titles...)
	pregap := musicbrainz.Track{Position: 0, Optional: true, Title: "Hidden Zeta"}
	release.Media[0].Tracks = append([]musicbrainz.Track{pregap}, release.Media[0].Tracks...)
	return release
}

// A pregap no file claims is not a track the folder lacks; one a file is tagged for is placed on it.
func TestFitTracklist_AnUnmappedPregapIsNotAMissingTrack(t *testing.T) {
	locals := []LocalTrack{{Track: 1, Title: Claim{Value: "Invented One", Provenance: ProvenanceTag}}, {Track: 2, Title: Claim{Value: "Invented Two", Provenance: ProvenanceTag}}}
	release := releaseWithPregap("Invented One", "Invented Two")

	fit := fitTracklist(locals, release, false)

	if fit.trackCount != 2 || fit.mapped != 2 || trackCount(release) != 2 {
		t.Errorf("want trackCount 2, mapped 2, trackCount(release) 2; got %d, %d, %d", fit.trackCount, fit.mapped, trackCount(release))
	}
	withHidden := append([]LocalTrack{{Title: Claim{Value: "Hidden Zeta", Provenance: ProvenanceTag}}}, locals...)
	fit = fitTracklist(withHidden, release, false)
	if fit.trackCount != 3 || fit.mapped != 3 || fit.tracks[0] == nil || fit.tracks[0].Position != 0 {
		t.Errorf("want the hidden file on position 0 with trackCount 3, got count %d, mapped %d, %+v", fit.trackCount, fit.mapped, fit.tracks[0])
	}
}

// Continuous numbering across discs counts the tracks owed, never the pregap before them.
func TestFitTracklist_ContinuousNumbersSkipThePregap(t *testing.T) {
	release := releaseWithPregap("Invented One", "Invented Two")
	release.Media = append(release.Media, musicbrainz.Medium{Position: 2, Tracks: []musicbrainz.Track{{Position: 1, Title: "Invented Three"}}})

	fit := fitTracklist([]LocalTrack{{Track: 3}}, release, false)

	if fit.mapped != 1 || len(fit.costs) != 1 || fit.costs[0] != 0 || fit.tracks[0].Disc != 2 {
		t.Errorf("want track 3 placed on disc 2 track 1 at no cost, got mapped %d costs %v", fit.mapped, fit.costs)
	}
}

func TestRefinementFor_APregapBecomesAnOptionalPartAndAddsNoRuntime(t *testing.T) {
	release := releaseWithPregap("Invented One")
	release.Media[0].Tracks[0].DurationMs = 61000
	release.Media[0].Tracks[1].DurationMs = 1000

	_, _, _, refinement, parts := refinementFor(musicbrainz.ReleaseGroup{}, release)

	if len(parts) != 2 || !parts[0].Optional || parts[0].MediaRef != "disc1:track0" || parts[0].SortKey != "00010000" || parts[1].Optional {
		t.Errorf("parts = %+v, want an optional disc1:track0 then a plain track", parts)
	}
	if refinement.RuntimeMs == nil || *refinement.RuntimeMs != 1000 {
		t.Errorf("RuntimeMs = %v, want 1000", refinement.RuntimeMs)
	}
}
