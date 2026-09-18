package library_test

import (
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
)

// placedUnit sets up a session with one committed unit whose files were placed
// by the given methods, and answers the session and unit ids.
func placedUnit(t *testing.T, opened *store.Store, methods []string) (int64, int64) {
	t.Helper()
	work := createWork(t, opened, "music", "Fake Album")
	ctx := t.Context()

	objects := make([]int64, len(methods))
	for index := range methods {
		object := createObject(t, opened, fmt.Sprintf("quick-%d", index), 10)
		addPath(t, opened, object.ID, 1, fmt.Sprintf("Artist/Album/track%d.flac", index))
		objects[index] = object.ID
	}

	sessions := library.NewImportSessions(opened)
	categoryID := int64(7)
	sessionID, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/x", Origin: library.ImportOriginManual, CategoryID: &categoryID,
		Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	unitIDs, err := sessions.AddUnits(ctx, sessionID, []library.NewUnit{
		{RelPath: "Artist/Album", Shape: library.UnitSet, FileCount: int64(len(methods)), Bytes: 10, EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	if err := sessions.RecordPlacement(ctx, unitIDs[0], library.Placement{
		WorkID: work.ID, WorkCreated: true, MediaObjectIDs: objects, TransferMethods: methods,
	}); err != nil {
		t.Fatalf("RecordPlacement: %v", err)
	}
	return sessionID, unitIDs[0]
}

// The method each file arrived by survives the write, which is the entire
// point: a reflink and a copy are indistinguishable on disk afterwards.
func TestUnitTransferMethods_RecordsHowFilesWerePlaced(t *testing.T) {
	opened, _ := newLibrary(t)
	sessionID, unitID := placedUnit(t, opened, []string{"reflink", "reflink", "copy"})

	byUnit, err := library.NewImportSessions(opened).UnitTransferMethods(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("UnitTransferMethods: %v", err)
	}
	counts := byUnit[unitID]
	if len(counts) != 2 {
		t.Fatalf("counts = %v, want one entry per distinct method", counts)
	}
	got := map[string]int64{}
	for _, count := range counts {
		got[count.Method] = count.Files
	}
	if got["reflink"] != 2 || got["copy"] != 1 {
		t.Errorf("counts = %v, want reflink=2 copy=1", got)
	}
}

// A caller that does not know the method writes nothing rather than a wrong
// guess, and the unit simply has no entry.
func TestUnitTransferMethods_AbsentWhenUnrecorded(t *testing.T) {
	opened, _ := newLibrary(t)
	sessionID, unitID := placedUnit(t, opened, nil)

	byUnit, err := library.NewImportSessions(opened).UnitTransferMethods(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("UnitTransferMethods: %v", err)
	}
	if counts, found := byUnit[unitID]; found {
		t.Errorf("counts = %v, want no entry for an unrecorded placement", counts)
	}
}
