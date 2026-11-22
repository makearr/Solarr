package music

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

func TestRefinementFor_CarriesEditionNameAndOnlyTrackCreditsThatDiffer(t *testing.T) {
	group := musicbrainz.ReleaseGroup{ArtistCredit: []musicbrainz.ArtistCredit{{Name: "Band"}}}
	release := musicbrainz.Release{
		Disambiguation: "deluxe edition", Packaging: "Digipak", Language: "eng", Script: "Latn",
		Media: []musicbrainz.Medium{{Position: 1, Title: "Live", Tracks: []musicbrainz.Track{
			{Position: 1, Title: "Own", ArtistCredit: []musicbrainz.ArtistCredit{{Name: "Band"}}},
			{Position: 2, Title: "Guest", ArtistCredit: []musicbrainz.ArtistCredit{{Name: "Band"}, {Name: "Singer"}}},
		}}},
	}

	_, _, _, refinement, parts := refinementFor(group, release)

	if refinement.Disambiguation != "deluxe edition" || refinement.Packaging != "Digipak" || refinement.Language != "eng" || refinement.Script != "Latn" {
		t.Errorf("refinement = %+v, want the edition's name, packaging, language and script", refinement)
	}
	got := []library.PartIdentity{parts[0], parts[1]}
	if got[0].ArtistCredit != "" || got[1].ArtistCredit != "Band & Singer" || got[0].MediumTitle != "Live" {
		t.Errorf("parts = %+v, want the album artist implied and the guest credit kept", got)
	}
}

func TestTrackNamingContext_TrackArtistFallsBackToTheAlbumArtist(t *testing.T) {
	own := TrackNaming{ArtistName: "Band", TrackArtistName: "Band & Singer", ReleaseDisambiguation: "deluxe edition", MediumTitle: "Live"}.Context()
	if own[TokenTrackArtistName] != "Band & Singer" || own[TokenTrackArtistCleanName] != "Band and Singer" ||
		own[TokenReleaseDisambiguation] != "deluxe edition" || own[TokenMediumTitle] != "Live" {
		t.Errorf("context = %v, want the track's own credit and the edition and disc names", own)
	}
	if got := (TrackNaming{ArtistName: "Band"}).Context()[TokenTrackArtistName]; got != "Band" {
		t.Errorf("track artist = %q, want the album artist when the track names none", got)
	}
}
