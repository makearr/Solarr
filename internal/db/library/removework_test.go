package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// An album nothing holds files for is deleted outright: it exists only as an
// intention, and the intention is what was withdrawn.
func TestRemoveWork_DeletesAWorkWithNoFiles(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Wanted Album")

	result, err := library.NewIdentity(opened).RemoveWork(t.Context(), work.ID)
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

	result, err := library.NewIdentity(opened).RemoveWork(t.Context(), work.ID)
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
