package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// The release a placement came from is kept with its files and read back with them; a file with no record reads as none (QUAL-1).
func TestRecordPlacement_KeepsTheReleaseWithTheFile(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Quiet Harbour")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)
	recorded := createObject(t, opened, "quick-recorded", 4096)
	scanned := createObject(t, opened, "quick-scanned", 4096)
	attach(t, opened, item.ID, recorded.ID, "primary", "")
	attach(t, opened, item.ID, scanned.ID, "primary", "")

	sessions := library.NewImportSessions(opened)
	ctx := t.Context()
	categoryID := int64(1)
	sessionID, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/x", Origin: library.ImportOriginManual, CategoryID: &categoryID, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	unitIDs, err := sessions.AddUnits(ctx, sessionID, []library.NewUnit{{RelPath: "a", Shape: library.UnitSet, FileCount: 1, Bytes: 1, EvidenceJSON: "{}"}})
	if err != nil {
		t.Fatal(err)
	}
	indexer := int64(3)
	if err := sessions.RecordPlacement(ctx, unitIDs[0], library.Placement{
		WorkID: work.ID, MediaObjectIDs: []int64{recorded.ID},
		Release: &library.ReleaseOrigin{Title: "Film.2019.1080p.WEB-DL-GRP", Source: "webdl", Group: "GRP", Revision: 2, IndexerID: &indexer, GrabbedAt: 90},
	}); err != nil {
		t.Fatal(err)
	}
	files, err := library.NewSeriesReader(opened).ItemFiles(ctx, item.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("ItemFiles = %d files, %v", len(files), err)
	}
	got, none := files[0].File, files[1].File
	if got.ReleaseSource != "webdl" || got.ReleaseGroup != "GRP" || got.Revision != 2 || !got.ReleaseKnown() {
		t.Errorf("the recorded file = %+v, want webdl, GRP, revision 2", got)
	}
	if none.ReleaseKnown() || none.ReleaseSource != "" || none.Revision != 0 {
		t.Errorf("the scanned file = %+v, want no release recorded", none)
	}
}
