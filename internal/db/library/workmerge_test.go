package library_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// An album held twice folds into the one with the edition: its files, wanted state, downloads, tags and ids follow,
// a file its tracklist has no place for stays the album's to place again, and the dropped album is gone.
func TestMergeWork_FoldsAnAlbumHeldTwiceIntoTheOther(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	keep := createWork(t, opened, "music", "Invented Album")
	drop := createWork(t, opened, "music", "Invented Album (Deluxe)")
	createPart(t, opened, keep.ID, "1:1", "1:001", "One")
	createPart(t, opened, keep.ID, "1:2", "1:002", "Two")
	keepItem := createDesired(t, opened, keep.ID, "SET", 2)
	dropItem := createDesired(t, opened, drop.ID, "SET", 2)
	placed := createObject(t, opened, "placed", 1)
	unplaced := createObject(t, opened, "unplaced", 1)
	attach(t, opened, dropItem.ID, placed.ID, "primary", "1:1")
	attach(t, opened, dropItem.ID, unplaced.ID, "primary", "9:9")
	download := createAcquisition(t, opened, dropItem.ID, "nzo-1", "QUEUED")
	tag := createTag(t, opened, "invented", 0)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, objectID := range []int64{placed.ID, unplaced.ID} {
			if err := q.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{WorkID: drop.ID, MediaObjectID: objectID, FirstSeenAt: 1}); err != nil {
				return err
			}
		}
		if err := q.AssignTag(ctx, librarydb.AssignTagParams{TagID: tag.ID, SubjectType: "work", SubjectID: drop.ID, Source: "user"}); err != nil {
			return err
		}
		return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: "musicbrainz:release-group", ExternalID: "retired-group", WorkID: drop.ID, Confidence: 1, FirstSeenAt: 1,
		})
	})

	preview, err := identity.PreviewWorkMerge(t.Context(), drop.ID, keep.ID)
	if err != nil {
		t.Fatalf("PreviewWorkMerge: %v", err)
	}
	want := library.WorkMerge{
		DropID: drop.ID, KeepID: keep.ID, DropTitle: drop.CanonicalTitle, KeepTitle: keep.CanonicalTitle,
		Files: 2, WantedItems: 1, Acquisitions: 1,
	}
	if preview != want {
		t.Errorf("preview = %+v, want %+v", preview, want)
	}
	if _, err := queries.GetWork(t.Context(), drop.ID); err != nil {
		t.Fatalf("the preview changed something: %v", err)
	}

	merged, err := identity.MergeWork(t.Context(), drop.ID, keep.ID)
	if err != nil || merged != want {
		t.Fatalf("MergeWork = %+v, %v; want %+v", merged, err, want)
	}
	if _, err := queries.GetWork(t.Context(), drop.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("the dropped work is still there: %v", err)
	}
	item, err := queries.GetDesiredItem(t.Context(), keepItem.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if item.State != "PARTIAL" || item.PresentCount != 1 {
		t.Errorf("kept item %s %d, want PARTIAL 1 (the file on track 1)", item.State, item.PresentCount)
	}
	files, err := queries.ListWorkFiles(t.Context(), keep.ID)
	if err != nil || len(files) != 2 {
		t.Errorf("kept work's files = %v, %v; want both, the unplaced one to place again", files, err)
	}
	moved, err := queries.GetAcquisition(t.Context(), download.ID)
	if err != nil || moved.DesiredItemID.Int64 != keepItem.ID {
		t.Errorf("download's item = %+v, %v; want %d", moved.DesiredItemID, err, keepItem.ID)
	}
	byID, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{Provider: "musicbrainz:release-group", ExternalID: "retired-group"})
	if err != nil || byID.ID != keep.ID {
		t.Errorf("the dropped id resolves to %d, %v; want %d", byID.ID, err, keep.ID)
	}
	titles, err := queries.ListWorkTitles(t.Context(), keep.ID)
	if err != nil || len(titles) != 1 || titles[0].Title != drop.CanonicalTitle || titles[0].Kind != "alias" {
		t.Errorf("kept work's titles = %+v, %v; want the dropped title as an alias", titles, err)
	}
	tagged := 0
	if err := opened.Read().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM tag_assignment WHERE subject_type = 'work' AND subject_id = ?`, keep.ID).Scan(&tagged); err != nil || tagged != 1 {
		t.Errorf("kept work's tags = %d, %v; want 1", tagged, err)
	}
}

func TestMergeWork_RefusesItselfAndAnotherType(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	album := createWork(t, opened, "music", "Invented Album")
	series := createWork(t, opened, "tv", "Invented Show")
	for _, into := range []int64{album.ID, series.ID} {
		if _, err := identity.MergeWork(t.Context(), album.ID, into); !errors.Is(err, library.ErrMergeRefused) {
			t.Errorf("MergeWork(%d into %d) = %v, want ErrMergeRefused", album.ID, into, err)
		}
	}
}

// A suggestion is what enrichment found; it goes with either work.
func TestSuggestWorkMerge_IsReadBackAndGoesWithTheWork(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	keep := createWork(t, opened, "music", "Invented Album")
	drop := createWork(t, opened, "music", "Invented Album")
	if err := identity.SuggestWorkMerge(t.Context(), drop.ID, keep.ID); err != nil {
		t.Fatalf("SuggestWorkMerge: %v", err)
	}
	got, found, err := identity.MergeSuggestionFor(t.Context(), drop.ID)
	if err != nil || !found || got != (library.MergeSuggestion{IntoWorkID: keep.ID, Title: keep.CanonicalTitle}) {
		t.Errorf("MergeSuggestionFor = %+v, %v, %v", got, found, err)
	}
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error { return q.DeleteWork(ctx, keep.ID) })
	if _, found, err := identity.MergeSuggestionFor(t.Context(), drop.ID); err != nil || found {
		t.Errorf("a suggestion outlived its work: %v, %v", found, err)
	}
}
