package music

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
)

func release(mbid, status, date string, titles ...string) musicbrainz.Release {
	tracks := make([]musicbrainz.Track, len(titles))
	for index, title := range titles {
		tracks[index] = musicbrainz.Track{Position: index + 1, Title: title}
	}
	return musicbrainz.Release{MBID: mbid, Status: status, Date: date, Media: []musicbrainz.Medium{{Position: 1, Tracks: tracks}}}
}

func files(titles ...string) []TrackFile {
	found := make([]TrackFile, len(titles))
	for index, title := range titles {
		found[index] = TrackFile{MediaObjectID: int64(index + 1), Disc: 1, Track: index + 1, Title: title}
	}
	return found
}

// Three tracks on disk fit the three-track album exactly, not its deluxe
// edition that merely contains them too.
func TestChooseRelease_PrefersTheTracklistClosestToTheFilesPresent(t *testing.T) {
	releases := []musicbrainz.Release{
		release("deluxe", "official", "2017-10-13", "Kebilegev Bibihi", "Semidos", "Kuselobi You Pato", "Bonus"),
		release("standard", "official", "2017-10-13", "Kebilegev Bibihi", "Semidos", "Kuselobi You Pato"),
	}

	chosen, ok := chooseRelease(releases, files("Kebilegev Bibihi", "Semidos", "Kuselobi You Pato"))
	if !ok || chosen.MBID != "standard" {
		t.Errorf("chose %q (ok=%v), want the standard edition", chosen.MBID, ok)
	}
}

// More files aligned beats a closer track count.
func TestChooseRelease_MostFilesAlignedWins(t *testing.T) {
	releases := []musicbrainz.Release{
		release("wrong", "official", "1991", "Detisisu Binip", "Something Else"),
		release("right", "official", "1991", "Detisisu Binip", "Covabapoz Fofu", "Pugabup", "Lafi of Moze", "Kusupus"),
	}

	chosen, ok := chooseRelease(releases, files("Detisisu Binip", "Covabapoz Fofu", "Pugabup"))
	if !ok || chosen.MBID != "right" {
		t.Errorf("chose %q (ok=%v), want the release three files align to", chosen.MBID, ok)
	}
}

func TestChooseRelease_PrefersAnOfficialReleaseAndThenTheEarliest(t *testing.T) {
	releases := []musicbrainz.Release{
		release("bootleg", "bootleg", "1990", "Pihudoc Zaze"),
		release("reissue", "official", "2009", "Pihudoc Zaze"),
		release("original", "official", "1988", "Pihudoc Zaze"),
	}

	chosen, ok := chooseRelease(releases, files("Pihudoc Zaze"))
	if !ok || chosen.MBID != "original" {
		t.Errorf("chose %q (ok=%v), want the earliest official release", chosen.MBID, ok)
	}
}

func TestChooseRelease_NothingWhenNoFileAligns(t *testing.T) {
	releases := []musicbrainz.Release{release("unrelated", "official", "2000", "Completely Different")}

	if chosen, ok := chooseRelease(releases, files("Detisisu Binip")); ok {
		t.Errorf("chose %q, want no choice when nothing aligns", chosen.MBID)
	}
}
