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
		library.NewRecycleBin(opened, fsys, fake), rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	result, err := revert.Revert(ctx, sessionID, library.Recycling{Retain: 168 * time.Hour}, 200)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if result.UnitsReverted != 1 || result.WorksDeleted != 1 || len(result.RecycledPaths) != 2 {
		t.Fatalf("result = %+v, want UnitsReverted=1 WorksDeleted=1 RecycledPaths=2 (track and lyrics)", result)
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
	if _, err := fsys.Stat(result.RecycledPaths[0].RecycledPath); err != nil {
		t.Errorf("recycle bin path does not exist: %v", err)
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
	if _, err := revert.Revert(ctx, sessionID, library.Recycling{Retain: 168 * time.Hour}, 300); !errors.Is(err, library.ErrSessionNotRevertible) {
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
		library.NewRecycleBin(opened, fsys, fake), rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	result, err := revert.Revert(ctx, sessionID, library.Recycling{Retain: 168 * time.Hour}, 200)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(result.SkippedSharedObjects) != 0 {
		t.Errorf("SkippedSharedObjects = %v, want none -- LibraryPathID makes this unambiguous", result.SkippedSharedObjects)
	}
	if len(result.RecycledPaths) != 1 {
		t.Fatalf("RecycledPaths = %d, want exactly 1 (only the session's own copy)", len(result.RecycledPaths))
	}

	// The session's own file: gone from disk and recycled.
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
		library.NewRecycleBin(opened, fsys, fake), rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	result, err := revert.Revert(ctx, sessionID, library.Recycling{Retain: 168 * time.Hour}, 200)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(result.SkippedSharedObjects) != 1 || result.SkippedSharedObjects[0] != object.ID {
		t.Errorf("SkippedSharedObjects = %v, want [%d]", result.SkippedSharedObjects, object.ID)
	}
	if len(result.RecycledPaths) != 0 {
		t.Errorf("RecycledPaths = %d, want 0 -- an ambiguous shared object must not be guessed at", len(result.RecycledPaths))
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
		library.NewRecycleBin(opened, fsys, fake), rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	result, err := revert.Revert(ctx, sessionID, library.Recycling{Retain: 168 * time.Hour}, 200)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if result.UnitsReverted != 2 {
		t.Errorf("UnitsReverted = %d, want 2", result.UnitsReverted)
	}
	if result.WorksDeleted != 1 {
		t.Errorf("WorksDeleted = %d, want exactly 1 (the work must not be deleted twice)", result.WorksDeleted)
	}
	if len(result.RecycledPaths) != 2 {
		t.Errorf("RecycledPaths = %d, want 2 (one per disc)", len(result.RecycledPaths))
	}
	if _, err := librarydb.New(opened.Read()).GetWork(ctx, work.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetWork after revert: err=%v, want sql.ErrNoRows", err)
	}
}

// failingRename is a file system that cannot move one named file, as a busy NAS cannot.
type failingRename struct {
	fsx.FS
	path    string
	enabled bool
}

func (f *failingRename) Rename(from, to string) error {
	if f.enabled && from == f.path {
		return errors.New("input/output error")
	}
	return f.FS.Rename(from, to)
}

// A revert that fails on one unit finishes the others and can be repeated: the failed unit still knows its work (REVERT-1).
func TestImportRevert_AFailedUnitKeepsItsWorkForTheRetry(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	mem := fsx.NewMem(fake)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()
	categoryID := int64(7)
	sessionID, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/x", Origin: library.ImportOriginManual, CategoryID: &categoryID, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var units []library.NewUnit
	for _, name := range []string{"One", "Two", "Three"} {
		units = append(units, library.NewUnit{RelPath: "Artist/" + name, Shape: library.UnitSet, FileCount: 1, Bytes: 4, EvidenceJSON: "{}"})
	}
	unitIDs, err := sessions.AddUnits(ctx, sessionID, units)
	if err != nil {
		t.Fatal(err)
	}
	var works []int64
	for index, name := range []string{"One", "Two", "Three"} {
		putTestFile(t, mem, "/media/root/Artist/"+name+"/track.flac", []byte("fake"))
		work := createWork(t, opened, "music", "Album "+name)
		item := createDesired(t, opened, work.ID, "SET", 1)
		object := createObject(t, opened, "quick-"+name, 4)
		addPath(t, opened, object.ID, 1, "Artist/"+name+"/track.flac")
		attach(t, opened, item.ID, object.ID, "primary", "disc1:track1")
		works = append(works, work.ID)
		if err := sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{{UnitID: unitIDs[index], Decision: library.UnitCommit, Confidence: 0.95}}); err != nil {
			t.Fatal(err)
		}
		if err := sessions.RecordPlacement(ctx, unitIDs[index], library.Placement{WorkID: work.ID, WorkCreated: true, MediaObjectIDs: []int64{object.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sessions.Finish(ctx, sessionID, library.ImportDone, 150, ""); err != nil {
		t.Fatal(err)
	}
	fsys := &failingRename{FS: mem, path: "/media/root/Artist/Two/track.flac", enabled: true}
	revert := library.NewImportRevert(
		sessions, library.NewPhysical(opened), library.NewIdentity(opened),
		library.NewRecycleBin(opened, fsys, fake), rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	recycling := library.Recycling{Retain: time.Hour}

	first, err := revert.Revert(ctx, sessionID, recycling, 200)
	if err == nil {
		t.Fatal("Revert succeeded though a file could not be moved")
	}
	if first.UnitsReverted != 2 || first.WorksDeleted != 2 {
		t.Fatalf("first revert = %+v, want the two other units done", first)
	}
	reader := librarydb.New(opened.Read())
	for index, want := range []bool{false, true, false} {
		_, err := reader.GetWork(ctx, works[index])
		if exists := err == nil; exists != want {
			t.Fatalf("work %d exists = %v, want %v", index+1, exists, want)
		}
	}
	listed, err := sessions.ListUnits(ctx, sessionID)
	if err != nil || listed[1].WorkID == nil || !listed[1].WorkCreated || listed[0].WorkID != nil || listed[2].WorkID != nil {
		t.Fatalf("units after the failed revert = %+v, %v; want only the second still carrying its work", listed, err)
	}

	fsys.enabled = false
	second, err := revert.Revert(ctx, sessionID, recycling, 300)
	if err != nil || second.UnitsReverted != 1 || second.WorksDeleted != 1 {
		t.Fatalf("second revert = %+v, %v; want the failed unit finished", second, err)
	}
	if _, err := reader.GetWork(ctx, works[1]); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("work 2 after the retry: %v, want gone", err)
	}
	if session, err := sessions.Get(ctx, sessionID); err != nil || session.State != library.ImportReverted {
		t.Errorf("session = %+v, %v; want REVERTED", session, err)
	}
}

// Reverting an import that replaced a file brings the replaced file back (REVERT-2).
func TestImportRevert_RestoresWhatTheRevertedFileHadReplaced(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	bin := library.NewRecycleBin(opened, fsys, fake)
	putTestFile(t, fsys, "/media/root/Artist/Album/old.flac", []byte("old"))
	putTestFile(t, fsys, "/media/root/Artist/Album/new.flac", []byte("better"))

	work := createWork(t, opened, "music", "Replaced Album")
	item := createDesired(t, opened, work.ID, "SET", 1)
	better := createObject(t, opened, "quick-better", 6)
	addPath(t, opened, better.ID, 1, "Artist/Album/new.flac")
	attach(t, opened, item.ID, better.ID, "primary", "disc1:track1")
	// The cutoff settle recycled the old file in favour of the better one.
	replacedBy := better.ID
	displaced, err := bin.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/Artist/Album/old.flac", Reason: library.ReasonReplaced,
		Retain: time.Hour, ReplacedByObjectID: &replacedBy,
	})
	if err != nil {
		t.Fatal(err)
	}

	sessions := library.NewImportSessions(opened)
	ctx := t.Context()
	categoryID := int64(7)
	sessionID, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/x", Origin: library.ImportOriginManual, CategoryID: &categoryID, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	unitIDs, err := sessions.AddUnits(ctx, sessionID, []library.NewUnit{{RelPath: "Artist/Album", Shape: library.UnitSet, FileCount: 1, Bytes: 6, EvidenceJSON: "{}"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{{UnitID: unitIDs[0], Decision: library.UnitCommit, Confidence: 0.95}}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.RecordPlacement(ctx, unitIDs[0], library.Placement{WorkID: work.ID, MediaObjectIDs: []int64{better.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Finish(ctx, sessionID, library.ImportDone, 150, ""); err != nil {
		t.Fatal(err)
	}
	revert := library.NewImportRevert(
		sessions, library.NewPhysical(opened), library.NewIdentity(opened), bin, rootPathLookup(map[int64]string{1: "/media/root"}),
	)
	result, err := revert.Revert(ctx, sessionID, library.Recycling{Retain: time.Hour}, 200)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(result.Restored) != 1 || result.Restored[0].ID != displaced.ID || len(result.NotRestored) != 0 {
		t.Fatalf("result = %+v, want the old file restored", result)
	}
	if _, err := fsys.Stat("/media/root/Artist/Album/old.flac"); err != nil {
		t.Errorf("the replaced file is not back: %v", err)
	}
	if _, err := fsys.Stat("/media/root/Artist/Album/new.flac"); err == nil {
		t.Error("the reverted file is still in the library")
	}
}
