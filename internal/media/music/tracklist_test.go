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

	file := music.ReadTrackFile(42, "", tags)

	if file.MediaObjectID != 42 || file.Disc != 2 || file.Track != 5 || file.Title != "A Song" {
		t.Fatalf("unexpected parse: %+v", file)
	}
	if file.RecordingMBID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("want recording mbid parsed, got %q", file.RecordingMBID)
	}
}

// boundIDs is the MediaObjectID bound to each part, 0 for a part left without a file.
func boundIDs(alignment music.Alignment) []int64 {
	ids := make([]int64, len(alignment.Parts))
	for i, aligned := range alignment.Parts {
		if aligned.File != nil {
			ids[i] = aligned.File.MediaObjectID
		}
	}
	return ids
}

func twoDiscParts() []music.Part {
	return []music.Part{
		part("disc1:track1", "Alpha Song", ""), part("disc1:track2", "Beta Song", ""),
		part("disc2:track1", "Gamma Song", ""), part("disc2:track2", "Delta Song", ""),
	}
}

func equalIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestAlign_DiscsInFoldersWithoutADiscTagBindByFolder(t *testing.T) {
	tags := mediatool.Tags{}
	files := []music.TrackFile{
		music.ReadTrackFile(1, "Set/CD1/01.flac", tags), music.ReadTrackFile(2, "Set/CD1/02.flac", tags),
		music.ReadTrackFile(3, "Set/CD2/01.flac", tags), music.ReadTrackFile(4, "Set/CD2/02.flac", tags),
	}
	if got := boundIDs(music.Align(twoDiscParts(), files)); !equalIDs(got, []int64{1, 2, 3, 4}) {
		t.Errorf("bound = %v, want [1 2 3 4]", got)
	}
}

func TestAlign_AnUntitledFileOfAnUnknownDiscDoesNotTakeDiscOnesSlot(t *testing.T) {
	parts := twoDiscParts()
	files := []music.TrackFile{{MediaObjectID: 7, Track: 1}}
	if got := boundIDs(music.Align(parts, files)); got[0] != 0 {
		t.Errorf("bound = %v, want the unknown-disc file kept off disc1:track1", got)
	}
}

func TestAlign_ContinuousNumberingAcrossDiscsBindsInSequence(t *testing.T) {
	files := []music.TrackFile{
		{MediaObjectID: 1, Track: 1, Title: "Alpha Song"}, {MediaObjectID: 2, Track: 2, Title: "Beta Song"},
		{MediaObjectID: 3, Track: 3, Title: "Gamma Song"}, {MediaObjectID: 4, Track: 4, Title: "Delta Song"},
	}
	result := music.Align(twoDiscParts(), files)
	if got := boundIDs(result); !equalIDs(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("bound = %v, want [1 2 3 4]", got)
	}
	if result.Parts[3].Basis != "sequence" || result.Parts[3].Confidence != 0.8 {
		t.Errorf("last part = %s/%.2f, want sequence/0.80", result.Parts[3].Basis, result.Parts[3].Confidence)
	}
}

func TestAlign_VinylPositionsBindBySideInOrder(t *testing.T) {
	parts := []music.Part{
		part("disc1:track1", "Alpha Song", ""), part("disc1:track2", "Beta Song", ""),
		part("disc1:track3", "Gamma Song", ""), part("disc1:track4", "Delta Song", ""),
	}
	files := []music.TrackFile{
		music.ReadTrackFile(1, "A1.flac", mediatool.Tags{"title": "Alpha Song"}), music.ReadTrackFile(2, "A2.flac", mediatool.Tags{"title": "Beta Song"}),
		music.ReadTrackFile(3, "B1.flac", mediatool.Tags{"title": "Gamma Song"}), music.ReadTrackFile(4, "B2.flac", mediatool.Tags{"title": "Delta Song"}),
	}
	if got := boundIDs(music.Align(parts, files)); !equalIDs(got, []int64{1, 2, 3, 4}) {
		t.Errorf("bound = %v, want [1 2 3 4]", got)
	}
}

func TestReadTrackFile_ReadsWhatTheTagsLeaveFromThePath(t *testing.T) {
	for relPath, want := range map[string]music.TrackFile{
		"03 - Some Title.flac": {Track: 3, TrackFrom: "name", Title: "Some Title"},
		"2-03 Some Title.flac": {Disc: 2, DiscFrom: "name", Track: 3, TrackFrom: "name", Title: "Some Title"},
		"203 Some Title.flac":  {Track: 203, TrackFrom: "name", Title: "Some Title"},
		"B2 Some Title.flac":   {Side: 2, SideTrack: 2, TrackFrom: "name", Title: "Some Title"},
		"CD2 - Live/07.flac":   {Disc: 2, DiscFrom: "folder", Track: 7, TrackFrom: "name"},
	} {
		got := music.ReadTrackFile(0, relPath, mediatool.Tags{})
		got.RelPath = ""
		if got != want {
			t.Errorf("%s: %+v, want %+v", relPath, got, want)
		}
	}
}

func TestAlign_ATrackNumberedByNameOnAMultiDiscAlbumReadsHundredsAsDisc(t *testing.T) {
	files := []music.TrackFile{
		music.ReadTrackFile(1, "101 Alpha Song.flac", nil), music.ReadTrackFile(2, "102 Beta Song.flac", nil),
		music.ReadTrackFile(3, "201 Gamma Song.flac", nil), music.ReadTrackFile(4, "202 Delta Song.flac", nil),
	}
	if got := boundIDs(music.Align(twoDiscParts(), files)); !equalIDs(got, []int64{1, 2, 3, 4}) {
		t.Errorf("bound = %v, want [1 2 3 4]", got)
	}
}

func TestAlign_ACloserTitleKeepsItsFileOverAnEarlierTrack(t *testing.T) {
	parts := []music.Part{part("disc1:track4", "Some Song", ""), part("disc1:track9", "Some Song (Live)", "")}
	files := []music.TrackFile{{MediaObjectID: 1, Title: "Some Song (Live)"}}
	if got := boundIDs(music.Align(parts, files)); !equalIDs(got, []int64{0, 1}) {
		t.Errorf("bound = %v, want the live file on the live track", got)
	}
}
