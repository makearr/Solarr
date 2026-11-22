package musicbrainz

import "testing"

func TestReleaseGroupQuery_AnArtistNarrowsTheSearchAndNoArtistLeavesItOut(t *testing.T) {
	if got, want := releaseGroupQuery("Kay", "Some Album"), `(artist:"Kay" OR artistname:"Kay") AND releasegroup:"Some Album"`; got != want {
		t.Errorf("with an artist = %s, want %s", got, want)
	}
	if got, want := releaseGroupQuery("", "Some Album"), `releasegroup:"Some Album"`; got != want {
		t.Errorf("without an artist = %s, want %s", got, want)
	}
}
