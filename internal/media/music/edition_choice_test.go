package music

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

func release(mbid, status, date string, titles ...string) musicbrainz.Release {
	tracks := make([]musicbrainz.Track, len(titles))
	for index, title := range titles {
		tracks[index] = musicbrainz.Track{Position: index + 1, Title: title}
	}
	return musicbrainz.Release{MBID: mbid, Status: status, Date: date, Media: []musicbrainz.Medium{{Position: 1, Tracks: tracks}}}
}

func files(titles ...string) []LocalTrack {
	found := make([]LocalTrack, len(titles))
	for index, title := range titles {
		found[index] = LocalTrack{Title: Claim{Value: title, Provenance: ProvenanceTag}, Disc: 1, Track: index + 1}
	}
	return found
}

var trauma = musicbrainz.ReleaseGroup{MBID: "rg", Title: "Kebilegev Bibihi", PrimaryType: "Album"}

// Three tracks on disk fit the three-track album exactly, not its deluxe
// edition that merely contains them too.
func TestChooseRelease_PrefersTheTracklistClosestToTheFilesPresent(t *testing.T) {
	releases := []musicbrainz.Release{
		release("deluxe", "official", "2017-10-13", "Kebilegev Bibihi", "Semidos", "Kuselobi You Pato", "Bonus"),
		release("standard", "official", "2017-10-13", "Kebilegev Bibihi", "Semidos", "Kuselobi You Pato"),
	}

	chosen, ok := chooseRelease(trauma, releases, files("Kebilegev Bibihi", "Semidos", "Kuselobi You Pato"))
	if want := (editionChoice{Release: releases[1], Fits: true}); !ok || !reflect.DeepEqual(chosen, want) {
		t.Errorf("chose %+v (ok=%v), want the standard edition", chosen, ok)
	}
}

// A release more files fit beats one with a closer track count.
func TestChooseRelease_MostFilesAlignedWins(t *testing.T) {
	releases := []musicbrainz.Release{
		release("wrong", "official", "1991", "Detisisu Binip", "Something Else"),
		release("right", "official", "1991", "Detisisu Binip", "Covabapoz Fofu", "Pugabup", "Lafi of Moze", "Kusupus"),
	}

	chosen, ok := chooseRelease(trauma, releases, files("Detisisu Binip", "Covabapoz Fofu", "Pugabup"))
	if want := (editionChoice{Release: releases[1], Fits: true}); !ok || !reflect.DeepEqual(chosen, want) {
		t.Errorf("chose %+v (ok=%v), want the release all three files fit", chosen, ok)
	}
}

func TestChooseRelease_PrefersAnOfficialReleaseAndThenTheEarliest(t *testing.T) {
	releases := []musicbrainz.Release{
		release("bootleg", "bootleg", "1990", "Pihudoc Zaze"),
		release("reissue", "official", "2009", "Pihudoc Zaze"),
		release("original", "official", "1988", "Pihudoc Zaze"),
	}

	chosen, ok := chooseRelease(trauma, releases, files("Pihudoc Zaze"))
	if want := (editionChoice{Release: releases[2], Fits: true}); !ok || !reflect.DeepEqual(chosen, want) {
		t.Errorf("chose %+v (ok=%v), want the earliest official release", chosen, ok)
	}
}

// The user's answer (b), 2026-09-28: a name-only match whose file fits no release still gets the
// best-ranked tracklist, marked as not fitting, rather than waiting for metadata for ever.
func TestChooseRelease_BestRankedWhenNoFileFits(t *testing.T) {
	releases := []musicbrainz.Release{
		release("bootleg", "bootleg", "2000", "Completely Different"),
		release("official", "official", "2001", "Completely Different"),
	}

	chosen, ok := chooseRelease(trauma, releases, files("Detisisu Binip"))
	if want := (editionChoice{Release: releases[1]}); !ok || !reflect.DeepEqual(chosen, want) {
		t.Errorf("chose %+v (ok=%v), want the official release, not fitting", chosen, ok)
	}
}

func TestChooseRelease_NothingWithoutATracklist(t *testing.T) {
	releases := []musicbrainz.Release{{MBID: "empty", Status: "official"}}

	if chosen, ok := chooseRelease(trauma, releases, files("Detisisu Binip")); ok {
		t.Errorf("chose %+v, want no choice when no release has tracks", chosen)
	}
}
