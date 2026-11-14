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

func countRows(t *testing.T, opened interface {
	Read() *sql.DB
}, query string, arguments ...any) int {
	t.Helper()
	var rows int
	if err := opened.Read().QueryRowContext(t.Context(), query, arguments...).Scan(&rows); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return rows
}

// Rows that name a work only by type and id have no foreign key; deleting the work must remove them itself.
func TestDeleteWork_RemovesTagsFavoritesAndArtworkNamingIt(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	doomed := createWork(t, opened, "music", "Invented Album")
	tag := createTag(t, opened, "invented", 0)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.AssignTag(ctx, librarydb.AssignTagParams{TagID: tag.ID, SubjectType: "work", SubjectID: doomed.ID, Source: "user"}); err != nil {
			return err
		}
		if err := q.AddFavorite(ctx, librarydb.AddFavoriteParams{UserID: 1, SubjectType: "work", SubjectID: doomed.ID, CreatedAt: 1}); err != nil {
			return err
		}
		if err := q.UpsertArtwork(ctx, librarydb.UpsertArtworkParams{SubjectType: "work", SubjectID: doomed.ID, Kind: "poster", CacheKey: "k"}); err != nil {
			return err
		}
		return q.RecordArtworkMiss(ctx, librarydb.RecordArtworkMissParams{SubjectID: doomed.ID, Source: "fanart", MissedAt: 1})
	})

	if err := identity.DeleteWork(t.Context(), doomed.ID); err != nil {
		t.Fatalf("DeleteWork: %v", err)
	}
	for _, table := range []string{"tag_assignment", "favorite", "artwork", "artwork_miss"} {
		if rows := countRows(t, opened, "SELECT count(*) FROM "+table+" WHERE subject_type = 'work' AND subject_id = ?", doomed.ID); rows != 0 {
			t.Errorf("%s: %d rows still name the deleted work", table, rows)
		}
	}
}

// A favourite, a release rule and a hand-made collection place are the user's own input and follow the merge.
func TestMergeWork_KeepsWhatTheUserDidToTheDroppedAlbum(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	keep := createWork(t, opened, "music", "Invented Album")
	drop := createWork(t, opened, "music", "Invented Album (Deluxe)")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.AddFavorite(ctx, librarydb.AddFavoriteParams{UserID: 1, SubjectType: "work", SubjectID: drop.ID, CreatedAt: 1})
	})
	err := opened.Write(t.Context(), 0, func(ctx context.Context, tx *sql.Tx) error {
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO work_release_filter (work_id, kind, value, created_at) VALUES (?, 'must_contain', 'invented', 1)", []any{drop.ID}},
			{"INSERT INTO collection (media_type, name, kind, source, added_at) VALUES ('music', 'Invented Set', 'manual', 'user', 1)", nil},
			{"INSERT INTO collection_member (collection_id, work_id, by_hand) VALUES (1, ?, 1)", []any{drop.ID}},
		} {
			if _, err := tx.ExecContext(ctx, statement.sql, statement.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := identity.MergeWork(t.Context(), drop.ID, keep.ID); err != nil {
		t.Fatalf("MergeWork: %v", err)
	}
	for name, query := range map[string]string{
		"favorite":      "SELECT count(*) FROM favorite WHERE subject_type = 'work' AND subject_id = ?",
		"release rule":  "SELECT count(*) FROM work_release_filter WHERE work_id = ?",
		"hand-made set": "SELECT count(*) FROM collection_member WHERE by_hand = 1 AND work_id = ?",
	} {
		if rows := countRows(t, opened, query, keep.ID); rows != 1 {
			t.Errorf("%s: %d rows on the kept work, want 1", name, rows)
		}
		if rows := countRows(t, opened, query, drop.ID); rows != 0 {
			t.Errorf("%s: %d rows still on the dropped work", name, rows)
		}
	}
}
