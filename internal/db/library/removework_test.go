package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// An album nothing holds files for is deleted outright: it exists only as an
// intention, and the intention is what was withdrawn.
func TestRemoveWork_DeletesAWorkWithNoFiles(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Wanted Album")

	result, err := library.NewIdentity(opened).RemoveWork(t.Context(), work.ID, false)
	if err != nil {
		t.Fatalf("RemoveWork: %v", err)
	}
	if !result.WorkDeleted || result.FilesKept != 0 {
		t.Errorf("result = %+v, want the work deleted and no files kept", result)
	}
}

// An album whose files are on disk is kept: the next scan would recreate it,
// so reporting it as deleted would be a lie.
func TestRemoveWork_KeepsAWorkThatStillHasFiles(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Album On Disk")
	object := createObject(t, opened, "quick-remove-1", 10)
	addPath(t, opened, object.ID, 1, "Artist/Album/track.flac")
	if err := queries.LinkWorkFile(t.Context(), librarydb.LinkWorkFileParams{
		WorkID: work.ID, MediaObjectID: object.ID, FirstSeenAt: 100,
	}); err != nil {
		t.Fatalf("LinkWorkFile: %v", err)
	}

	result, err := library.NewIdentity(opened).RemoveWork(t.Context(), work.ID, false)
	if err != nil {
		t.Fatalf("RemoveWork: %v", err)
	}
	if result.WorkDeleted {
		t.Error("a work with files must not be deleted")
	}
	if result.FilesKept != 1 {
		t.Errorf("FilesKept = %d, want 1", result.FilesKept)
	}
	if _, err := queries.GetWork(t.Context(), work.ID); err != nil {
		t.Errorf("the work should still exist: %v", err)
	}
}

// Found live 2026-09-25: an album that was downloaded could not be removed, because its download
// record still pointed at the want. The record stays, as history, detached from it.
func TestRemoveWork_KeepsTheDownloadRecordOfAnAlbumItRemoves(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Downloaded Album")
	item := createDesired(t, opened, work.ID, "SET", 10)
	acquisitions := library.NewAcquisitions(opened)
	itemID := item.ID
	created, err := acquisitions.Create(t.Context(), library.NewAcquisition{
		DesiredItemID: &itemID, CandidateTitle: "Artist-Downloaded.Album-FLAC", DownloaderID: 1,
		DownloadClientRef: "nzo-1", Protocol: "usenet", State: "CLEANED", CreatedAt: 1, UpdatedAt: 1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The import that placed it records the album too.
	sessions := library.NewImportSessions(opened)
	sessionID, err := sessions.Create(t.Context(), library.NewSession{SourcePath: "/downloads/x", Origin: "acquisition", Threshold: 0.9})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	unitIDs, err := sessions.AddUnits(t.Context(), sessionID, []library.NewUnit{{RelPath: ".", Shape: "SET", EvidenceJSON: "{}"}})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetImportUnitPlacement(ctx, librarydb.SetImportUnitPlacementParams{
			WorkID: sql.NullInt64{Int64: work.ID, Valid: true}, ID: unitIDs[0],
		})
	})

	result, err := library.NewIdentity(opened).RemoveWork(t.Context(), work.ID, false)
	if err != nil {
		t.Fatalf("RemoveWork: %v", err)
	}
	kept, err := acquisitions.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !result.WorkDeleted || kept.DesiredItemID != nil || kept.CandidateTitle != "Artist-Downloaded.Album-FLAC" {
		t.Errorf("result %+v, acquisition %+v; want the work gone and the record kept, detached", result, kept)
	}
}

// A work whose type's scan leaves excluded works out goes although its files stay, as Sonarr's delete does.
func TestRemoveWork_WithFilesDeletesTheWorkAndKeepsTheFiles(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "tv", "Series On Disk")
	object := createObject(t, opened, "remove-with-files-1", 10)
	addPath(t, opened, object.ID, 1, "Series/S01/e01.mkv")
	if err := queries.LinkWorkFile(t.Context(), librarydb.LinkWorkFileParams{WorkID: work.ID, MediaObjectID: object.ID, FirstSeenAt: 100}); err != nil {
		t.Fatalf("LinkWorkFile: %v", err)
	}
	result, err := library.NewIdentity(opened).RemoveWork(t.Context(), work.ID, true)
	if err != nil || result != (library.RemoveWorkResult{WorkDeleted: true, FilesKept: 1}) {
		t.Fatalf("RemoveWork = %+v, %v; want the work deleted, its one file kept", result, err)
	}
	if _, err := queries.GetWork(t.Context(), work.ID); err == nil {
		t.Error("the work is still there")
	}
	if _, err := queries.GetMediaObject(t.Context(), object.ID); err != nil {
		t.Errorf("the file's record went with the work: %v", err)
	}
}
