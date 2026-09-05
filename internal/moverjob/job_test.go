package moverjob_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mover"
	"github.com/makearr/MediaNexus/internal/moverjob"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

// fakeRoots is a fixed, in-memory root-folder table -- the same "caller
// supplies pre-fetched facts" shape mover.Plan's own Request.Roots uses.
type fakeRoots map[int64]moverjob.RootFolder

func (r fakeRoots) RootFolder(id int64) (moverjob.RootFolder, bool) {
	root, ok := r[id]
	return root, ok
}

type fixture struct {
	fsys  *fsx.Mem
	store *store.Store
	clock *clock.Fake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	memory := fsx.NewMem(fake)

	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	return &fixture{fsys: memory, store: opened, clock: fake}
}

func (f *fixture) writeFile(t *testing.T, absolute string, contents string) {
	t.Helper()
	if err := f.fsys.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := f.fsys.Create(absolute)
	if err != nil {
		t.Fatalf("Create(%q): %v", absolute, err)
	}
	if _, err := file.Write([]byte(contents)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func (f *fixture) readFile(t *testing.T, absolute string) string {
	t.Helper()
	file, err := f.fsys.Open(absolute)
	if err != nil {
		t.Fatalf("Open(%q): %v", absolute, err)
	}
	defer func() { _ = file.Close() }()
	data := make([]byte, 4096)
	n, _ := file.Read(data)
	return string(data[:n])
}

func (f *fixture) exists(absolute string) bool {
	_, err := f.fsys.Stat(absolute)
	return err == nil
}

// registerObject creates one media_object with a library_path at
// rootFolderID/relPath, returning its id.
func (f *fixture) registerObject(t *testing.T, rootFolderID int64, relPath, contents string) int64 {
	t.Helper()
	physical := library.NewPhysical(f.store)
	ids, err := physical.RegisterBatch(t.Context(), rootFolderID, []library.PhysicalFile{{
		RelPath: relPath, SizeBytes: int64(len(contents)),
		QuickHash: relPath, FirstSeenAt: f.clock.Now(),
	}})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	return ids[0]
}

func (f *fixture) job(roots moverjob.RootFolders) *moverjob.Job {
	return moverjob.NewJob(moverjob.Config{
		FS: f.fsys, MoveJobs: library.NewMoveJobs(f.store), Physical: library.NewPhysical(f.store),
		Roots: roots, Clock: f.clock, Logger: slog.New(slog.DiscardHandler),
	})
}

func (f *fixture) jobWithDrainCompletion(roots moverjob.RootFolders, completion moverjob.DrainCompletion) *moverjob.Job {
	return moverjob.NewJob(moverjob.Config{
		FS: f.fsys, MoveJobs: library.NewMoveJobs(f.store), Physical: library.NewPhysical(f.store),
		Roots: roots, DrainCompletion: completion, Clock: f.clock, Logger: slog.New(slog.DiscardHandler),
	})
}

// fakeDrainCompletion records whether and how it was called, without
// needing a real settings-backed Roots/Files pair -- moverjob only cares
// that the hook fires for the right job, not what it does.
type fakeDrainCompletion struct {
	calls            int
	lastRootFolderID int64
	lastMoveJobID    int64
}

func (f *fakeDrainCompletion) CompleteDrainIfEmpty(_ context.Context, rootFolderID, moveJobID int64) error {
	f.calls++
	f.lastRootFolderID, f.lastMoveJobID = rootFolderID, moveJobID
	return nil
}

func (f *fixture) run(t *testing.T, job *moverjob.Job, moveJobID int64) error {
	t.Helper()
	data, err := json.Marshal(moverjob.Payload{MoveJobID: moveJobID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return job.Run(t.Context(), jobs.Job{Type: moverjob.JobType, Payload: data})
}

// TestJob_MovesAFileCrossFilesystemAndRecordsIt exercises the copy/verify
// path (ADR-51's own COPY/VERIFY/RECORD/UNLINK, Invariant 77): different
// FSIDs, so ApplyOne copies rather than renaming.
func TestJob_MovesAFileCrossFilesystemAndRecordsIt(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(t, "/root1/Artist/Album/track.flac", "the bytes")
	objectID := fx.registerObject(t, 1, "Artist/Album/track.flac", "the bytes")

	moveJobs := library.NewMoveJobs(fx.store)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonCategory), FilesTotal: 1, BytesTotal: 9,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{{
		MediaObjectID: objectID, AffinityKey: "artist:1", DestRootID: 2,
		DestRelPath: "Artist/Album/track.flac", Bytes: 9,
	}}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	roots := fakeRoots{1: {Path: "/root1", FSID: "fs-a"}, 2: {Path: "/root2", FSID: "fs-b"}}
	if err := fx.run(t, fx.job(roots), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := fx.readFile(t, "/root2/Artist/Album/track.flac"); got != "the bytes" {
		t.Errorf("dest content = %q, want %q", got, "the bytes")
	}
	if fx.exists("/root1/Artist/Album/track.flac") {
		t.Error("source file still exists after a completed move")
	}

	physical := library.NewPhysical(fx.store)
	paths, err := physical.PathsForObject(t.Context(), objectID)
	if err != nil {
		t.Fatalf("PathsForObject: %v", err)
	}
	if len(paths) != 1 || paths[0].RootFolderID != 2 || paths[0].RelPath != "Artist/Album/track.flac" {
		t.Fatalf("paths = %+v, want the one path moved to root 2", paths)
	}
	if paths[0].MediaObjectID != objectID {
		t.Error("media_object_id changed across a relocation -- Invariant 70 violated")
	}

	job, err := moveJobs.Get(t.Context(), jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != library.MoveJobDone || job.FilesDone != 1 || job.BytesDone != 9 {
		t.Errorf("job = %+v, want DONE/1/9", job)
	}
	items, err := moveJobs.ListItems(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].State != library.MoveItemDone {
		t.Fatalf("items = %+v, want one DONE item", items)
	}
}

// TestJob_MovesAFileSameFilesystemViaRename exercises the rename(2) path:
// both roots share an FSID, so ApplyOne renames atomically instead of
// copying.
func TestJob_MovesAFileSameFilesystemViaRename(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(t, "/root1/Old/track.flac", "same-fs bytes")
	objectID := fx.registerObject(t, 1, "Old/track.flac", "same-fs bytes")

	moveJobs := library.NewMoveJobs(fx.store)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonRename), FilesTotal: 1, BytesTotal: 13,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{{
		MediaObjectID: objectID, DestRootID: 1, DestRelPath: "New/track.flac", Bytes: 13,
	}}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	roots := fakeRoots{1: {Path: "/root1", FSID: "fs-a"}}
	if err := fx.run(t, fx.job(roots), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !fx.exists("/root1/New/track.flac") {
		t.Error("renamed file missing at its new path")
	}
	if fx.exists("/root1/Old/track.flac") {
		t.Error("source file still exists after a same-filesystem rename")
	}
	if fx.exists("/root1/Old") {
		t.Error("now-empty source directory was not tidied away")
	}
}

// TestJob_SkipsRelocationWhenTheItemIsAlreadyAtItsDestination covers the
// resume case where a prior attempt completed RECORD (library_path already
// points at the destination) but crashed before the item was marked DONE --
// move_item does not persist the old source location, so the job cannot
// unlink it again; it can only finish the bookkeeping.
func TestJob_SkipsRelocationWhenTheItemIsAlreadyAtItsDestination(t *testing.T) {
	fx := newFixture(t)
	objectID := fx.registerObject(t, 2, "Artist/Album/track.flac", "already moved")

	moveJobs := library.NewMoveJobs(fx.store)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonCategory), FilesTotal: 1, BytesTotal: 13,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{{
		MediaObjectID: objectID, DestRootID: 2, DestRelPath: "Artist/Album/track.flac", Bytes: 13,
	}}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	// Deliberately no source file on disk and no root 1 in fakeRoots -- if
	// the job tried to treat this as a fresh move it would fail looking up
	// a source root, proving the already-there branch is what actually ran.
	roots := fakeRoots{2: {Path: "/root2", FSID: "fs-b"}}
	if err := fx.run(t, fx.job(roots), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	items, err := moveJobs.ListItems(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].State != library.MoveItemDone {
		t.Fatalf("items = %+v, want one DONE item", items)
	}
}

// TestJob_OneItemFailureDoesNotStopTheJob matches import.inspect's own
// "one bad unit does not fail the batch" reasoning: a move to an unknown
// root fails that one item without preventing the other from completing.
func TestJob_OneItemFailureDoesNotStopTheJob(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(t, "/root1/ok.flac", "fine")
	okID := fx.registerObject(t, 1, "ok.flac", "fine")
	fx.writeFile(t, "/root1/broken.flac", "boom")
	brokenID := fx.registerObject(t, 1, "broken.flac", "boom")

	moveJobs := library.NewMoveJobs(fx.store)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonRebalance), FilesTotal: 2, BytesTotal: 8,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{
		{MediaObjectID: okID, DestRootID: 2, DestRelPath: "ok.flac", Bytes: 4},
		{MediaObjectID: brokenID, DestRootID: 99, DestRelPath: "broken.flac", Bytes: 4},
	}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	roots := fakeRoots{1: {Path: "/root1", FSID: "fs-a"}, 2: {Path: "/root2", FSID: "fs-b"}}
	if err := fx.run(t, fx.job(roots), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	items, err := moveJobs.ListItems(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	states := map[int64]string{}
	for _, item := range items {
		states[item.MediaObjectID] = item.State
	}
	if states[okID] != library.MoveItemDone {
		t.Errorf("ok item state = %q, want DONE", states[okID])
	}
	if states[brokenID] != library.MoveItemFailed {
		t.Errorf("broken item state = %q, want FAILED", states[brokenID])
	}

	job, err := moveJobs.Get(t.Context(), jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != library.MoveJobDone {
		t.Errorf("job.State = %q, want DONE even with one failed item", job.State)
	}
}

// TestJob_TidyStopsAtANonEmptyDirectory proves TIDY does not reach past a
// sibling file still using the parent directory.
func TestJob_TidyStopsAtANonEmptyDirectory(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(t, "/root1/Artist/Album1/track.flac", "one")
	fx.writeFile(t, "/root1/Artist/Album2/other.flac", "two")
	objectID := fx.registerObject(t, 1, "Artist/Album1/track.flac", "one")

	moveJobs := library.NewMoveJobs(fx.store)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonCategory), FilesTotal: 1, BytesTotal: 3,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{{
		MediaObjectID: objectID, DestRootID: 2, DestRelPath: "Artist/Album1/track.flac", Bytes: 3,
	}}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	roots := fakeRoots{1: {Path: "/root1", FSID: "fs-a"}, 2: {Path: "/root2", FSID: "fs-b"}}
	if err := fx.run(t, fx.job(roots), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if fx.exists("/root1/Artist/Album1") {
		t.Error("now-empty Album1 directory was not tidied away")
	}
	if !fx.exists("/root1/Artist") {
		t.Error("Artist directory was removed despite Album2 still using it")
	}
	if !fx.exists("/root1/Artist/Album2/other.flac") {
		t.Error("sibling file was disturbed by TIDY")
	}
}

// TestJob_TidyNeverRemovesTheRootFolderItself covers the one case where the
// source root becomes genuinely empty after a move: TIDY must stop at the
// root's own directory, never remove it -- deleting a root folder's own
// directory entry out from under settings.db would break every future
// operation against it, distinct from the "stopped by a non-empty sibling"
// case TestJob_TidyStopsAtANonEmptyDirectory already covers.
func TestJob_TidyNeverRemovesTheRootFolderItself(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(t, "/root1/OnlyAlbum/track.flac", "only file")
	objectID := fx.registerObject(t, 1, "OnlyAlbum/track.flac", "only file")

	moveJobs := library.NewMoveJobs(fx.store)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonCategory), FilesTotal: 1, BytesTotal: 9,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{{
		MediaObjectID: objectID, DestRootID: 2, DestRelPath: "OnlyAlbum/track.flac", Bytes: 9,
	}}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	roots := fakeRoots{1: {Path: "/root1", FSID: "fs-a"}, 2: {Path: "/root2", FSID: "fs-b"}}
	if err := fx.run(t, fx.job(roots), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !fx.exists("/root1") {
		t.Error("TIDY removed the root folder's own directory, not just its now-empty contents")
	}
}

// TestJob_CallsDrainCompletionForADrainJobWithASourceRoot proves the
// completion hook fires with the right (source root, move job) pair once a
// DRAIN job finishes -- the real gap a live container run found: nothing
// ever checked back to advance drain_state once the move itself succeeded.
func TestJob_CallsDrainCompletionForADrainJobWithASourceRoot(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(t, "/root1/track.flac", "bytes")
	objectID := fx.registerObject(t, 1, "track.flac", "bytes")

	moveJobs := library.NewMoveJobs(fx.store)
	sourceRootID := int64(1)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonDrain), SourceRootID: &sourceRootID, FilesTotal: 1, BytesTotal: 5,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{{
		MediaObjectID: objectID, DestRootID: 2, DestRelPath: "track.flac", Bytes: 5,
	}}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	completion := &fakeDrainCompletion{}
	roots := fakeRoots{1: {Path: "/root1", FSID: "fs-a"}, 2: {Path: "/root2", FSID: "fs-b"}}
	if err := fx.run(t, fx.jobWithDrainCompletion(roots, completion), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if completion.calls != 1 {
		t.Fatalf("DrainCompletion called %d times, want 1", completion.calls)
	}
	if completion.lastRootFolderID != 1 || completion.lastMoveJobID != jobID {
		t.Errorf("DrainCompletion called with (%d, %d), want (1, %d)",
			completion.lastRootFolderID, completion.lastMoveJobID, jobID)
	}
}

// TestJob_DoesNotCallDrainCompletionForANonDrainReason proves the hook is
// specific to DRAIN jobs -- a rename/category/rebalance job's source root
// has no drain to complete.
func TestJob_DoesNotCallDrainCompletionForANonDrainReason(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(t, "/root1/track.flac", "bytes")
	objectID := fx.registerObject(t, 1, "track.flac", "bytes")

	moveJobs := library.NewMoveJobs(fx.store)
	sourceRootID := int64(1)
	jobID, err := moveJobs.Create(t.Context(), library.NewMoveJob{
		Reason: string(mover.ReasonRebalance), SourceRootID: &sourceRootID, FilesTotal: 1, BytesTotal: 5,
		PlanJSON: "{}", CreatedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := moveJobs.AddItems(t.Context(), jobID, []library.NewMoveItem{{
		MediaObjectID: objectID, DestRootID: 2, DestRelPath: "track.flac", Bytes: 5,
	}}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	completion := &fakeDrainCompletion{}
	roots := fakeRoots{1: {Path: "/root1", FSID: "fs-a"}, 2: {Path: "/root2", FSID: "fs-b"}}
	if err := fx.run(t, fx.jobWithDrainCompletion(roots, completion), jobID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if completion.calls != 0 {
		t.Errorf("DrainCompletion called %d times for a REBALANCE job, want 0", completion.calls)
	}
}

func TestActionKeyFor_DiffersPerMoveJob(t *testing.T) {
	if moverjob.ActionKeyFor(7) == moverjob.ActionKeyFor(8) {
		t.Error("ActionKeyFor does not distinguish different move jobs")
	}
}
