package library_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

func TestImportRevert_UndoesAPlacedUnitAndDeletesTheWorkItCreated(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	putTestFile(t, fsys, "/media/root/Artist/Album/track.flac", []byte("fake audio"))

	work := createWork(t, opened, "music", "Fake Album")
	item := createDesired(t, opened, work.ID, "SET", 1)
	object := createObject(t, opened, "quick-1", int64(len("fake audio")))
	addPath(t, opened, object.ID, 1, "Artist/Album/track.flac")
	attach(t, opened, item.ID, object.ID, "primary", "disc1:track1")

	sessions := library.NewImportSessions(opened)
	ctx := t.Context()
	categoryID := int64(7)
	sessionID, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/x", Origin: library.ImportOriginManual, CategoryID: &categoryID,
		Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	unitIDs, err := sessions.AddUnits(ctx, sessionID, []library.NewUnit{
		{RelPath: "Artist/Album", Shape: library.UnitSet, FileCount: 1, Bytes: int64(len("fake audio")), EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	if err := sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{
		{UnitID: unitIDs[0], Decision: library.UnitCommit, Confidence: 0.95},
	}); err != nil {
		t.Fatalf("SetUnitDecisions: %v", err)
	}
	if err := sessions.RecordPlacement(ctx, unitIDs[0], library.Placement{
		WorkID: work.ID, WorkCreated: true, MediaObjectIDs: []int64{object.ID},
	}); err != nil {
		t.Fatalf("RecordPlacement: %v", err)
	}
	if err := sessions.Finish(ctx, sessionID, library.ImportDone, 150, ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	revert := library.NewImportRevert(
		sessions, library.NewPhysical(opened), library.NewIdentity(opened),
		library.NewQuarantine(opened, fsys, fake), rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	result, err := revert.Revert(ctx, sessionID, 168*time.Hour, 200)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if result.UnitsReverted != 1 || result.WorksDeleted != 1 || len(result.QuarantinedPaths) != 1 {
		t.Fatalf("result = %+v, want UnitsReverted=1 WorksDeleted=1 QuarantinedPaths=1", result)
	}

	if _, err := fsys.Stat("/media/root/Artist/Album/track.flac"); err == nil {
		t.Error("original file still exists after Revert")
	}
	if _, err := fsys.Stat(result.QuarantinedPaths[0].QuarantinePath); err != nil {
		t.Errorf("quarantine path does not exist: %v", err)
	}

	if _, err := librarydb.New(opened.Read()).GetMediaObject(ctx, object.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetMediaObject after revert: err=%v, want sql.ErrNoRows", err)
	}
	if _, err := librarydb.New(opened.Read()).GetWork(ctx, work.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetWork after revert: err=%v, want sql.ErrNoRows -- Revert must delete a work it created", err)
	}
	if _, err := librarydb.New(opened.Read()).GetDesiredItem(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDesiredItem after revert: err=%v, want sql.ErrNoRows (cascaded from the deleted work)", err)
	}

	units, err := sessions.ListUnits(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 1 || units[0].WorkID != nil || units[0].WorkCreated {
		t.Errorf("unit after revert = %+v, want WorkID nil and WorkCreated false", units[0])
	}

	session, err := sessions.Get(ctx, sessionID)
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if session.State != library.ImportReverted {
		t.Errorf("session.State = %q, want %q", session.State, library.ImportReverted)
	}

	// A second revert must refuse -- there is nothing left to undo.
	if _, err := revert.Revert(ctx, sessionID, 168*time.Hour, 300); !errors.Is(err, library.ErrSessionNotRevertible) {
		t.Errorf("second Revert = %v, want ErrSessionNotRevertible", err)
	}
}

// The trickiest correctness case: two units in the same session resolve to
// the same work (a MULTI_DISC pair GROUP happened to split into two units,
// say) -- the first to run creates it, the second only matches it. Revert
// must delete the work exactly once, and must not foreign-key-violate on
// the second unit's own now-stale import_unit.work_id, which is exactly
// why ClearImportUnitPlacement runs for every unit before any work/edition
// is deleted rather than one unit at a time.
func TestImportRevert_TwoUnitsSharingAWorkDeletesItExactlyOnce(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	putTestFile(t, fsys, "/media/root/Artist/Album/disc1.flac", []byte("disc one"))
	putTestFile(t, fsys, "/media/root/Artist/Album/disc2.flac", []byte("disc two"))

	work := createWork(t, opened, "music", "Fake Double Album")
	object1 := createObject(t, opened, "quick-1", int64(len("disc one")))
	object2 := createObject(t, opened, "quick-2", int64(len("disc two")))
	addPath(t, opened, object1.ID, 1, "Artist/Album/disc1.flac")
	addPath(t, opened, object2.ID, 1, "Artist/Album/disc2.flac")

	sessions := library.NewImportSessions(opened)
	ctx := t.Context()
	categoryID := int64(7)
	sessionID, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/x", Origin: library.ImportOriginManual, CategoryID: &categoryID,
		Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	unitIDs, err := sessions.AddUnits(ctx, sessionID, []library.NewUnit{
		{RelPath: "Artist/Album/disc1", Shape: library.UnitSingle, FileCount: 1, Bytes: 8, EvidenceJSON: "{}"},
		{RelPath: "Artist/Album/disc2", Shape: library.UnitSingle, FileCount: 1, Bytes: 8, EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	if err := sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{
		{UnitID: unitIDs[0], Decision: library.UnitCommit, Confidence: 0.95},
		{UnitID: unitIDs[1], Decision: library.UnitCommit, Confidence: 0.95},
	}); err != nil {
		t.Fatalf("SetUnitDecisions: %v", err)
	}
	// unit 0 created the work; unit 1 only matched it -- exactly the shape
	// ResolveWorksDetailed produces for a second unit resolving to the
	// same external id within one session.
	if err := sessions.RecordPlacement(ctx, unitIDs[0], library.Placement{
		WorkID: work.ID, WorkCreated: true, MediaObjectIDs: []int64{object1.ID},
	}); err != nil {
		t.Fatalf("RecordPlacement unit 0: %v", err)
	}
	if err := sessions.RecordPlacement(ctx, unitIDs[1], library.Placement{
		WorkID: work.ID, WorkCreated: false, MediaObjectIDs: []int64{object2.ID},
	}); err != nil {
		t.Fatalf("RecordPlacement unit 1: %v", err)
	}
	if err := sessions.Finish(ctx, sessionID, library.ImportDone, 150, ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	revert := library.NewImportRevert(
		sessions, library.NewPhysical(opened), library.NewIdentity(opened),
		library.NewQuarantine(opened, fsys, fake), rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	result, err := revert.Revert(ctx, sessionID, 168*time.Hour, 200)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if result.UnitsReverted != 2 {
		t.Errorf("UnitsReverted = %d, want 2", result.UnitsReverted)
	}
	if result.WorksDeleted != 1 {
		t.Errorf("WorksDeleted = %d, want exactly 1 (the work must not be deleted twice)", result.WorksDeleted)
	}
	if len(result.QuarantinedPaths) != 2 {
		t.Errorf("QuarantinedPaths = %d, want 2 (one per disc)", len(result.QuarantinedPaths))
	}
	if _, err := librarydb.New(opened.Read()).GetWork(ctx, work.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetWork after revert: err=%v, want sql.ErrNoRows", err)
	}
}
