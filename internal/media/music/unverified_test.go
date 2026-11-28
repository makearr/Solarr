package music_test

import (
	"path"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// D6: a folder nothing identified is still an album in the library, named by its tags, with its
// artist, never searched for; identified later, its files move onto the real album and it goes.
func TestUnverifiedAlbum_IsAddedThenMovedOntoTheIdentifiedOne(t *testing.T) {
	committer, opened, fsys := newCommitter(t, true)
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", []byte("one"))
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/02 Conat.flac", []byte("two"))
	unit := testUnit("Katupoken/KI Pecezinu", []inspect.File{
		{RelPath: "Katupoken/KI Pecezinu/01 Sesoge.flac", SizeBytes: 3},
		{RelPath: "Katupoken/KI Pecezinu/02 Conat.flac", SizeBytes: 3},
	})
	tags := music.UnitTagSourceFor(commitSourceRoot, []mediatool.Probe{
		{Path: path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/01 Sesoge.flac"),
			Tags: mediatool.Tags{"album": "KI Pecezinu", "albumartist": "Katupoken", "tracknumber": "1", "title": "Sesoge"}},
		{Path: path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/02 Conat.flac"),
			Tags: mediatool.Tags{"album": "KI Pecezinu", "albumartist": "Katupoken", "tracknumber": "2", "title": "Conat"}},
	})
	queries := librarydb.New(opened.Read())

	unverified, err := committer.CommitUnverifiedUnit(t.Context(), commitSourceRoot, unit, tags, commitCategoryID, nil)
	if err != nil {
		t.Fatalf("CommitUnverifiedUnit: %v", err)
	}
	ids, err := queries.ListWorkExternalIDs(t.Context(), unverified.WorkID)
	if err != nil || len(ids) != 1 || ids[0].Provider != music.ProviderLocalAlbum {
		t.Fatalf("external ids = %+v, %v; want one %s id and no provider's", ids, err, music.ProviderLocalAlbum)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), unverified.WorkID)
	if err != nil || len(items) == 0 || items[0].Monitored != 0 {
		t.Fatalf("items = %+v, %v; want the album item unmonitored (D17)", items, err)
	}
	credits, err := queries.ListCreditsForWorkWithPerson(t.Context(), unverified.WorkID)
	if err != nil || len(credits) != 1 || credits[0].PersonName != "Katupoken" {
		t.Fatalf("credits = %+v, %v; want the tagged artist", credits, err)
	}

	identified, err := committer.IdentifyUnverified(t.Context(), unverified.WorkID, model.Identification{
		Strategy: "manual", Confidence: 1,
		ExternalID: &model.ExternalID{Provider: music.ProviderReleaseGroup, ExternalID: releaseGroupMBID, Confidence: 1},
	}, commitCategoryID)
	if err != nil {
		t.Fatalf("IdentifyUnverified: %v", err)
	}
	var linked int
	if err := opened.Read().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM work_file WHERE work_id = ?", identified.WorkID).Scan(&linked); err != nil || linked != 2 {
		t.Errorf("files on the identified album = %d (%v), want both", linked, err)
	}
	if _, err := queries.GetWork(t.Context(), unverified.WorkID); err == nil {
		t.Error("the unverified album is still there after its files moved")
	}
	if work, err := queries.GetWork(t.Context(), identified.WorkID); err != nil || work.CanonicalTitle != "KI Pecezinu" {
		t.Errorf("identified album = %+v, %v; want it named as the unverified one was", work, err)
	}
}

// A downloaded video lands as the scan records one: placed under its artist, one unverified video work, credited, no wanted item.
func TestCommitVideoUnit_PlacesAnUnverifiedVideoWorkUnderItsArtist(t *testing.T) {
	committer, opened, fsys := newCommitter(t, true)
	writeCommitSourceFile(t, fsys, "Rel/Katupoken - Sesoge (2011).mkv", []byte("video"))
	unit := testUnit("Rel", []inspect.File{{RelPath: "Rel/Katupoken - Sesoge (2011).mkv", SizeBytes: 5}})
	probes := []mediatool.Probe{{Path: path.Join(commitSourceRoot, "Rel/Katupoken - Sesoge (2011).mkv"), Measured: mediatool.Measured{Duration: 4 * time.Minute}}}

	placed, err := committer.CommitVideoUnit(t.Context(), commitSourceRoot, unit, probes, []*music.VideoIdentity{nil}, commitCategoryID, nil)
	if err != nil {
		t.Fatalf("CommitVideoUnit: %v", err)
	}
	queries := librarydb.New(opened.Read())
	work, err := queries.GetWork(t.Context(), placed.WorkID)
	if err != nil || work.CanonicalTitle != "Sesoge" || work.Subtype.String != music.SubtypeMusicVideo {
		t.Fatalf("work = %+v, %v; want the music video Sesoge", work, err)
	}
	ids, err := queries.ListWorkExternalIDs(t.Context(), placed.WorkID)
	if err != nil || len(ids) != 1 || ids[0].Provider != music.ProviderLocalVideo {
		t.Fatalf("external ids = %+v, %v; want one %s id", ids, err, music.ProviderLocalVideo)
	}
	credits, err := queries.ListCreditsForWorkWithPerson(t.Context(), placed.WorkID)
	if err != nil || len(credits) != 1 || credits[0].PersonName != "Katupoken" {
		t.Fatalf("credits = %+v, %v; want the artist of the name", credits, err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), placed.WorkID)
	if err != nil || len(items) != 0 {
		t.Errorf("items = %+v, %v; want none", items, err)
	}
	if len(placed.MediaObjectIDs) != 1 || len(placed.LibraryPathIDs) != 1 {
		t.Errorf("placed = %+v, want one file", placed)
	}
}

// A video MusicBrainz named is that recording's work, tied to the release group of its track.
func TestCommitVideoUnit_ANamedMusicVideoIsItsRecordingAndLinksItsAlbum(t *testing.T) {
	committer, opened, fsys := newCommitter(t, true)
	writeCommitSourceFile(t, fsys, "Rel/clip.mkv", []byte("video"))
	unit := testUnit("Rel", []inspect.File{{RelPath: "Rel/clip.mkv", SizeBytes: 5}})
	probes := []mediatool.Probe{{Tags: mediatool.Tags{"artist": "Katupoken", "title": "Sesoge"}}}
	named := &music.VideoIdentity{Provider: music.ProviderVideoRecording, ExternalID: "rec-1", Title: "Sesoge", ReleaseGroupMBID: "rg-1", Confidence: 0.97}

	placed, err := committer.CommitVideoUnit(t.Context(), commitSourceRoot, unit, probes, []*music.VideoIdentity{named}, commitCategoryID, nil)
	if err != nil {
		t.Fatalf("CommitVideoUnit: %v", err)
	}
	ids, err := librarydb.New(opened.Read()).ListWorkExternalIDs(t.Context(), placed.WorkID)
	if err != nil {
		t.Fatalf("ListWorkExternalIDs: %v", err)
	}
	var got []string
	for _, id := range ids {
		got = append(got, id.Provider+"="+id.ExternalID)
	}
	want := []string{music.ProviderVideoRecording + "=rec-1", music.ProviderVideoAlbum + "=" + music.VideoAlbumID("rg-1", "rec-1")}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("external ids = %v, want %v", got, want)
	}
}
