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
	if fit := fitTracklist(locals, tracklist(titles...)); fit.supported != 10 {
		t.Errorf("supported = %d, want all 10", fit.supported)
	}
}

// A live track is another recording: its file pairs with it, not with the studio take.
func TestFitTracklist_ALiveQualifierStillTellsRecordingsApart(t *testing.T) {
	locals := []LocalTrack{
		{Track: 2, Title: Claim{Value: "Invented Song (Live)", Provenance: ProvenanceTag}},
	}
	fit := fitTracklist(locals, tracklist("Invented Song", "Invented Song (Live)"))
	if fit.tracks[0] == nil || fit.tracks[0].Position != 2 {
		t.Errorf("tracks[0] = %+v, want the live track at position 2", fit.tracks[0])
	}
}
