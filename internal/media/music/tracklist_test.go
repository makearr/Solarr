package music_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func part(mediaRef, title, recordingMBID string) music.Part {
	return music.Part{MediaRef: mediaRef, Title: title, RecordingMBID: recordingMBID}
}

func TestAlign_RecordingMBIDMatchesEvenWithWrongPositionAndTitle(t *testing.T) {
	parts := []music.Part{
		part("disc1:track1", "Track One", "11111111-1111-1111-1111-111111111111"),
	}
	candidates := []music.TrackFile{
		{MediaObjectID: 9, Disc: 1, Track: 9, Title: "Completely Different",
			RecordingMBID: "11111111-1111-1111-1111-111111111111"},
	}

	result := music.Align(parts, candidates)

	if result.Parts[0].File == nil || result.Parts[0].File.MediaObjectID != 9 {
		t.Fatalf("recording MBID match did not align, got %+v", result.Parts[0])
	}
	if result.Parts[0].Confidence != 1.0 || result.Parts[0].Basis != "recording-mbid" {
		t.Errorf("want confidence 1.0/recording-mbid, got %.2f/%s",
			result.Parts[0].Confidence, result.Parts[0].Basis)
	}
}

func TestAlign_PositionMatchesWhenNoRecordingMBID(t *testing.T) {
	parts := []music.Part{part("disc1:track2", "Second Song", "")}
	candidates := []music.TrackFile{{MediaObjectID: 5, Disc: 1, Track: 2, Title: "Second Song"}}

	result := music.Align(parts, candidates)

	if result.Parts[0].File == nil || result.Parts[0].File.MediaObjectID != 5 {
		t.Fatalf("position match did not align, got %+v", result.Parts[0])
	}
	if result.Parts[0].Basis != "position" {
		t.Errorf("want basis position, got %s", result.Parts[0].Basis)
	}
}

func TestAlign_BonusTrackShiftsNumberingButTitleStillMatches(t *testing.T) {
	// The official tracklist is two songs. The download has a bonus track
	// inserted first, shifting the real second track to physical position 3.
	parts := []music.Part{
		part("disc1:track1", "Opener", ""),
		part("disc1:track2", "Closer", ""),
	}
	candidates := []music.TrackFile{
		{MediaObjectID: 1, Disc: 1, Track: 1, Title: "Opener"},
		{MediaObjectID: 2, Disc: 1, Track: 2, Title: "Bonus Remix"},
		{MediaObjectID: 3, Disc: 1, Track: 3, Title: "Closer"},
	}

	result := music.Align(parts, candidates)

	if result.Parts[0].File == nil || result.Parts[0].File.MediaObjectID != 1 {
		t.Fatalf("opener: want media object 1, got %+v", result.Parts[0])
	}
	if result.Parts[1].File == nil || result.Parts[1].File.MediaObjectID != 3 {
		t.Fatalf("closer: want the title-matched file 3 despite the position mismatch, got %+v",
			result.Parts[1])
	}
	if result.Parts[1].Basis != "title" {
		t.Errorf("want basis title, got %s", result.Parts[1].Basis)
	}
	if len(result.Extra) != 1 || result.Extra[0].MediaObjectID != 2 {
		t.Fatalf("want the bonus track left over as Extra, got %+v", result.Extra)
	}
}

func TestAlign_UnrelatedTitleBelowThresholdStaysUnmatched(t *testing.T) {
	parts := []music.Part{part("disc1:track1", "Symphony No. 5", "")}
	candidates := []music.TrackFile{{MediaObjectID: 1, Title: "Totally Unrelated Track"}}

	result := music.Align(parts, candidates)

	if result.Parts[0].File != nil {
		t.Fatalf("want no match below threshold, got %+v", result.Parts[0])
	}
	if len(result.Extra) != 1 {
		t.Fatalf("unmatched candidate should surface as Extra, got %d", len(result.Extra))
	}
}

func TestAlign_MissingPartsStayUnmatchedWithoutConsumingCandidates(t *testing.T) {
	parts := []music.Part{
		part("disc1:track1", "Have It", ""),
		part("disc1:track2", "Missing It", ""),
	}
	candidates := []music.TrackFile{{MediaObjectID: 1, Disc: 1, Track: 1, Title: "Have It"}}

	result := music.Align(parts, candidates)

	if result.Parts[0].File == nil {
		t.Fatal("track 1 should have matched")
	}
	if result.Parts[1].File != nil {
		t.Fatalf("track 2 has no candidate and must stay unmatched, got %+v", result.Parts[1])
	}
	if len(result.Extra) != 0 {
		t.Fatalf("no candidate should be left over, got %+v", result.Extra)
	}
}

func TestAlign_TwoDiscsDoNotCrossMatchByTrackNumberAlone(t *testing.T) {
	parts := []music.Part{
		part("disc1:track1", "Disc One Track One", ""),
		part("disc2:track1", "Disc Two Track One", ""),
	}
	candidates := []music.TrackFile{
		{MediaObjectID: 1, Disc: 2, Track: 1, Title: "Disc Two Track One"},
		{MediaObjectID: 2, Disc: 1, Track: 1, Title: "Disc One Track One"},
	}

	result := music.Align(parts, candidates)

	if result.Parts[0].File == nil || result.Parts[0].File.MediaObjectID != 2 {
		t.Fatalf("disc1:track1 should match media object 2, got %+v", result.Parts[0])
	}
	if result.Parts[1].File == nil || result.Parts[1].File.MediaObjectID != 1 {
		t.Fatalf("disc2:track1 should match media object 1, got %+v", result.Parts[1])
	}
}

func TestReadTrackFile_ParsesSlashedTrackAndDiscTags(t *testing.T) {
	tags := mediatool.Tags{
		"TRACKNUMBER":         "5/12",
		"DISCNUMBER":          "2/2",
		"TITLE":               "A Song",
		"MUSICBRAINZ_TRACKID": "11111111-1111-1111-1111-111111111111",
	}

	file := music.ReadTrackFile(42, tags)

	if file.MediaObjectID != 42 || file.Disc != 2 || file.Track != 5 || file.Title != "A Song" {
		t.Fatalf("unexpected parse: %+v", file)
	}
	if file.RecordingMBID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("want recording mbid parsed, got %q", file.RecordingMBID)
	}
}
