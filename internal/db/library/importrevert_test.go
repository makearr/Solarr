package library_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestImportRevert_UndoesAPlacedUnitAndDeletesTheWorkItCreated(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	putTestFile(t, fsys, "/media/root/Artist/Album/track.flac", []byte("fake audio"))
	putTestFile(t, fsys, "/media/root/Artist/Album/track.lrc", []byte("lyrics"))

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
		// The cue sheet was deleted by hand since: revert skips it rather than failing.
		Extras: []library.UnitExtra{{RootFolderID: 1, RelPath: "Artist/Album/track.lrc"}, {RootFolderID: 1, RelPath: "Artist/Album/gone.cue"}},
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
	if result.UnitsReverted != 1 || result.WorksDeleted != 1 || len(result.QuarantinedPaths) != 2 {
		t.Fatalf("result = %+v, want UnitsReverted=1 WorksDeleted=1 QuarantinedPaths=2 (track and lyrics)", result)
	}
	if _, err := fsys.Stat("/media/root/Artist/Album/track.lrc"); err == nil {
		t.Error("extra file still exists after Revert")
	}
	if extras, err := sessions.UnitExtras(ctx, sessionID); err != nil || len(extras) != 0 {
		t.Errorf("UnitExtras after revert = %v, %v, want none", extras, err)
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
// The 4.22 linking-audit bug, reproduced: a second, unrelated import reuses an
// existing media_object via ADR-11's quick-hash dedupe (a duplicate track shipped in
// two releases, say -- library_path_id is what RegisterBatch always creates fresh, the
// object it points at is what may be shared). Reverting the second session must leave
// the first, untouched library entry -- and the shared media_object itself -- alone.
func TestImportRevert_SharedObjectFromAnUnrelatedCommitSurvives(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	putTestFile(t, fsys, "/media/root/Existing/Album/track.flac", []byte("shared bytes"))
	putTestFile(t, fsys, "/media/root/New/Compilation/track.flac", []byte("shared bytes"))

	object := createObject(t, opened, "quick-shared", int64(len("shared bytes")))
	existingPathID := addPath(t, opened, object.ID, 1, "Existing/Album/track.flac")
	newPathID := addPath(t, opened, object.ID, 1, "New/Compilation/track.flac")

	work := createWork(t, opened, "music", "Fake Compilation")

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
		{RelPath: "New/Compilation", Shape: library.UnitSet, FileCount: 1, Bytes: int64(len("shared bytes")), EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	if err := sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{
		{UnitID: unitIDs[0], Decision: library.UnitCommit, Confidence: 0.95},
	}); err != nil {
		t.Fatalf("SetUnitDecisions: %v", err)
	}
	// Exactly what CommitUnit's own RegisterBatch produces when it dedupes onto an
	// object another commit already owns: a MediaObjectID it does not own alone,
	// paired with the LibraryPathID that is genuinely this unit's own new row.
	if err := sessions.RecordPlacement(ctx, unitIDs[0], library.Placement{
		WorkID: work.ID, WorkCreated: true,
		MediaObjectIDs: []int64{object.ID}, LibraryPathIDs: []int64{newPathID},
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
	if len(result.SkippedSharedObjects) != 0 {
		t.Errorf("SkippedSharedObjects = %v, want none -- LibraryPathID makes this unambiguous", result.SkippedSharedObjects)
	}
	if len(result.QuarantinedPaths) != 1 {
		t.Fatalf("QuarantinedPaths = %d, want exactly 1 (only the session's own copy)", len(result.QuarantinedPaths))
	}

	// The session's own file: gone from disk and quarantined.
	if _, err := fsys.Stat("/media/root/New/Compilation/track.flac"); err == nil {
		t.Error("the session's own placed file still exists after Revert")
	}
	if _, err := librarydb.New(opened.Read()).GetPathByLocation(ctx, librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "New/Compilation/track.flac",
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("the session's own library_path row (%d) still exists after Revert", newPathID)
	}

	// The unrelated, pre-existing file: untouched on disk, in the database, and by name.
	if _, err := fsys.Stat("/media/root/Existing/Album/track.flac"); err != nil {
		t.Errorf("an unrelated library entry sharing these bytes was deleted by an unrelated revert: %v", err)
	}
	existingPath, err := librarydb.New(opened.Read()).GetPathByLocation(ctx, librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "Existing/Album/track.flac",
	})
	if err != nil {
		t.Fatalf("the unrelated library_path row (%d) was deleted by an unrelated revert: %v", existingPathID, err)
	}
	if existingPath.ID != existingPathID || existingPath.MediaObjectID != object.ID {
		t.Errorf("existing path = %+v, want id=%d media_object_id=%d unchanged", existingPath, existingPathID, object.ID)
	}

	// The shared media_object must survive: the unrelated path still references it.
	if _, err := librarydb.New(opened.Read()).GetMediaObject(ctx, object.ID); err != nil {
		t.Errorf("shared media_object %d was deleted while another path still referenced it: %v", object.ID, err)
	}
}

// Data written before migration 0029 has no LibraryPathID to disambiguate with. When the
// object still has more than one path at revert time, ImportRevert must refuse to guess
// which one is its own rather than risk repeating the bug -- neither path is touched.
func TestImportRevert_AmbiguousSharedObjectWithoutALibraryPathIDIsSkipped(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	putTestFile(t, fsys, "/media/root/Existing/Album/track.flac", []byte("shared bytes"))
	putTestFile(t, fsys, "/media/root/New/Compilation/track.flac", []byte("shared bytes"))

	object := createObject(t, opened, "quick-shared", int64(len("shared bytes")))
	addPath(t, opened, object.ID, 1, "Existing/Album/track.flac")
	addPath(t, opened, object.ID, 1, "New/Compilation/track.flac")

	work := createWork(t, opened, "music", "Fake Compilation")

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
		{RelPath: "New/Compilation", Shape: library.UnitSet, FileCount: 1, Bytes: int64(len("shared bytes")), EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	if err := sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{
		{UnitID: unitIDs[0], Decision: library.UnitCommit, Confidence: 0.95},
	}); err != nil {
		t.Fatalf("SetUnitDecisions: %v", err)
	}
	// No LibraryPathIDs: the pre-migration shape.
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
	if len(result.SkippedSharedObjects) != 1 || result.SkippedSharedObjects[0] != object.ID {
		t.Errorf("SkippedSharedObjects = %v, want [%d]", result.SkippedSharedObjects, object.ID)
	}
	if len(result.QuarantinedPaths) != 0 {
		t.Errorf("QuarantinedPaths = %d, want 0 -- an ambiguous shared object must not be guessed at", len(result.QuarantinedPaths))
	}
	if _, err := fsys.Stat("/media/root/Existing/Album/track.flac"); err != nil {
		t.Errorf("the unrelated file was removed despite the ambiguous skip: %v", err)
	}
	if _, err := fsys.Stat("/media/root/New/Compilation/track.flac"); err != nil {
		t.Errorf("the session's own file was removed despite the ambiguous skip: %v", err)
	}
	if _, err := librarydb.New(opened.Read()).GetMediaObject(ctx, object.ID); err != nil {
		t.Errorf("the shared media_object was deleted despite the ambiguous skip: %v", err)
	}
}

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
