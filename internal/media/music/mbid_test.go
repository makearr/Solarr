package music_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

const (
	releaseGroupMBID = "2a6bec3f-b635-464a-8072-f462e388ab05"
	releaseMBID      = "b330cb50-d587-4928-8cfe-08561c1c7791"
	recordingMBID    = "78307c86-75a3-4695-8c83-90f4ed301f8b"
	artistMBID       = "35264db6-189f-40c5-8f04-f3b1a3f1e6d1"
)

func TestReadMBIDs_ReadsEveryLevelFromVorbisNames(t *testing.T) {
	tags := mediatool.Tags{
		"musicbrainz_releasegroupid": releaseGroupMBID,
		"musicbrainz_albumid":        releaseMBID,
		"musicbrainz_trackid":        recordingMBID,
		"musicbrainz_albumartistid":  artistMBID,
	}

	got := music.ReadMBIDs(tags)
	// ADR-29: reading only one level loses either which album this is, or which
	// pressing.
	if got.ReleaseGroup != releaseGroupMBID || got.Release != releaseMBID {
		t.Errorf("work/edition = %q/%q, want %q/%q",
			got.ReleaseGroup, got.Release, releaseGroupMBID, releaseMBID)
	}
	if got.Recording != recordingMBID {
		t.Errorf("Recording = %q, want %q", got.Recording, recordingMBID)
	}
	if !slices.Equal(got.AlbumArtists, []string{artistMBID}) {
		t.Errorf("AlbumArtists = %v, want [%s]", got.AlbumArtists, artistMBID)
	}
}

// The same tag arrives spelled three ways depending on the container, verified
// against ffprobe (documentation/media-tools.md).
func TestReadMBIDs_ReadsTheID3AndKid3SpellingsToo(t *testing.T) {
	for name, tags := range map[string]mediatool.Tags{
		"ID3 and MP4":   {"musicbrainz release group id": releaseGroupMBID},
		"kid3 Vorbis":   {"MUSICBRAINZ RELEASE GROUP ID": releaseGroupMBID},
		"Picard Vorbis": {"MUSICBRAINZ_RELEASEGROUPID": releaseGroupMBID},
	} {
		if got := music.ReadMBIDs(tags).ReleaseGroup; got != releaseGroupMBID {
			t.Errorf("%s: ReleaseGroup = %q, want %q", name, got, releaseGroupMBID)
		}
	}
}

// A malformed identity is worse than none: it would be stored, matched against, and
// quietly wrong.
func TestReadMBIDs_RejectsAnythingThatIsNotAnMBID(t *testing.T) {
	for name, value := range map[string]string{
		"too short":         "5b11f4ce-a62d-471e-81fc-a69a8278c75",
		"too long":          releaseGroupMBID + "1",
		"not hexadecimal":   "5b11f4ce-a62d-471e-81fc-a69a8278c75z",
		"hyphens misplaced": "5b11f4cea-62d-471e-81fc-a69a8278c751",
		"a title":           "Tender Prey",
		"empty":             "",
	} {
		tags := mediatool.Tags{"musicbrainz_releasegroupid": value}
		if got := music.ReadMBIDs(tags).ReleaseGroup; got != "" {
			t.Errorf("%s (%q) was accepted as %q", name, value, got)
		}
	}
}

func TestReadMBIDs_NormalisesCaseAndSurroundingSpace(t *testing.T) {
	tags := mediatool.Tags{"musicbrainz_releasegroupid": "  2A6BEC3F-B635-464A-8072-F462E388AB05 "}
	if got := music.ReadMBIDs(tags).ReleaseGroup; got != releaseGroupMBID {
		t.Errorf("ReleaseGroup = %q, want the lowercased %q", got, releaseGroupMBID)
	}
}

// A collaboration carries several artist ids in one tag, and ffprobe hands back the
// joined string.
func TestReadMBIDs_SplitsAMultiValuedArtistTag(t *testing.T) {
	second := "d9311fda-65b8-4825-820c-9d6235d961c2"
	for separator, joined := range map[string]string{
		"slash":     artistMBID + "/" + second,
		"semicolon": artistMBID + "; " + second,
		"comma":     artistMBID + ", " + second,
	} {
		got := music.ReadMBIDs(mediatool.Tags{"musicbrainz_artistid": joined}).Artists
		if !slices.Equal(got, []string{artistMBID, second}) {
			t.Errorf("%s: Artists = %v, want both in order", separator, got)
		}
	}
	// One bad value in the list must not discard the good ones.
	mixed := music.ReadMBIDs(mediatool.Tags{"musicbrainz_artistid": "not-an-mbid/" + artistMBID}).Artists
	if !slices.Equal(mixed, []string{artistMBID}) {
		t.Errorf("Artists = %v, want just the valid one", mixed)
	}
}

func TestMBIDsEmpty_IsTrueOnlyWhenNothingWasFound(t *testing.T) {
	if !music.ReadMBIDs(mediatool.Tags{"artist": "Nick Cave"}).Empty() {
		t.Error("tags with no MusicBrainz ids did not report empty")
	}
	if music.ReadMBIDs(mediatool.Tags{"musicbrainz_artistid": artistMBID}).Empty() {
		t.Error("an artist id alone reported empty")
	}
}
