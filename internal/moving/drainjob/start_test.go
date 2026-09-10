package drainjob_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/moving/drainjob"
	"github.com/makearr/MediaNexus/internal/moving/moverjob"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/migrations"
)

func i64(v int64) *int64 { return &v }

func onlineRoot(id int64, freeBytes int64) settings.RootFolder {
	return settings.RootFolder{ID: id, Online: true, Writable: true, FreeBytes: i64(freeBytes), TotalBytes: i64(freeBytes * 2)}
}

// fakeRoots is an in-memory settings.RootFolder/CategoryRoot table.
type fakeRoots struct {
	folders     map[int64]settings.RootFolder
	attachments []settings.CategoryRoot
}

func (r *fakeRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	folder, ok := r.folders[id]
	return folder, ok
}

func (r *fakeRoots) CategoryRoots() []settings.CategoryRoot { return r.attachments }

func (r *fakeRoots) SetAcceptsNewForRootFolder(_ context.Context, rootFolderID int64, acceptsNew bool) error {
	for i, a := range r.attachments {
		if a.RootFolderID == rootFolderID {
			r.attachments[i].AcceptsNew = acceptsNew
		}
	}
	return nil
}

func (r *fakeRoots) SetRootFolderDrainState(_ context.Context, id int64, state string, drainJobID *int64) error {
	folder := r.folders[id]
	folder.DrainState = state
	folder.DrainJobID = drainJobID
	r.folders[id] = folder
	return nil
}

type fakeFiles map[int64][]library.FileOnRoot

func (f fakeFiles) PathsForRoot(_ context.Context, rootFolderID int64) ([]library.FileOnRoot, error) {
	return f[rootFolderID], nil
}

type fakeMoveJob struct {
	Reason     string
	FilesTotal int64
	BytesTotal int64
	Items      []library.NewMoveItem
}

type fakeMoveJobs struct {
	created fakeMoveJob
	nextID  int64
}

func (m *fakeMoveJobs) Create(_ context.Context, params library.NewMoveJob) (int64, error) {
	m.nextID = 1
	m.created = fakeMoveJob{Reason: params.Reason, FilesTotal: params.FilesTotal, BytesTotal: params.BytesTotal}
	return m.nextID, nil
}

func (m *fakeMoveJobs) AddItems(_ context.Context, jobID int64, items []library.NewMoveItem) error {
	if jobID != m.nextID {
		return errors.New("unexpected job id")
	}
	m.created.Items = items
	return nil
}

func newTestQueue(t *testing.T) *jobs.Queue {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "jobs", Path: filepath.Join(directory, "jobs.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Jobs()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	registry := jobs.NewRegistry()
	registry.MustRegister(moverjob.Definition)
	return jobs.NewQueue(opened, fake, registry, jobs.NoJitter)
}

func TestStart_PreviewComputesThePlanWithoutPersistingAnything(t *testing.T) {
	roots := &fakeRoots{
		folders: map[int64]settings.RootFolder{1: onlineRoot(1, 0), 2: onlineRoot(2, 1000)},
		attachments: []settings.CategoryRoot{
			{CategoryID: 10, RootFolderID: 1, AcceptsNew: true},
			{CategoryID: 10, RootFolderID: 2, AcceptsNew: true},
		},
	}
	files := fakeFiles{1: {{MediaObjectID: 1, RelPath: "Artist/track.flac", SizeBytes: 100}}}
	moveJobs := &fakeMoveJobs{}
	cfg := drainjob.Config{
		Roots: roots, Files: files, MoveJobs: moveJobs, Queue: newTestQueue(t), Clock: clock.NewFakeAtEpoch(),
	}

	result, err := drainjob.Start(t.Context(), cfg, 1, true)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Accepted || result.MoveJobID != 0 {
		t.Errorf("result = %+v, want a preview with nothing committed", result)
	}
	if len(result.Plan.Moves) != 1 {
		t.Fatalf("Plan.Moves = %+v, want one computed move", result.Plan.Moves)
	}
	if moveJobs.created.Reason != "" {
		t.Error("MoveJobs.Create was called during a preview")
	}
	if roots.folders[1].DrainState != "" {
		t.Errorf("drain state = %q, want untouched during a preview", roots.folders[1].DrainState)
	}
}

func TestStart_CommitsPersistsAndEnqueuesAndSetsDrainState(t *testing.T) {
	roots := &fakeRoots{
		folders: map[int64]settings.RootFolder{1: onlineRoot(1, 0), 2: onlineRoot(2, 1000)},
		attachments: []settings.CategoryRoot{
			{CategoryID: 10, RootFolderID: 1, AcceptsNew: true},
			{CategoryID: 10, RootFolderID: 2, AcceptsNew: true},
		},
	}
	files := fakeFiles{1: {
		{MediaObjectID: 1, RelPath: "Artist/01.flac", SizeBytes: 100},
		{MediaObjectID: 2, RelPath: "Artist/02.flac", SizeBytes: 100},
	}}
	moveJobs := &fakeMoveJobs{}
	cfg := drainjob.Config{
		Roots: roots, Files: files, MoveJobs: moveJobs, Queue: newTestQueue(t), Clock: clock.NewFakeAtEpoch(),
	}

	result, err := drainjob.Start(t.Context(), cfg, 1, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !result.Accepted || result.MoveJobID == 0 {
		t.Fatalf("result = %+v, want an accepted, persisted plan", result)
	}
	if len(moveJobs.created.Items) != 2 {
		t.Fatalf("MoveJobs got %d items, want 2", len(moveJobs.created.Items))
	}
	if roots.folders[1].DrainState != settings.DrainStateDraining {
		t.Errorf("drain state = %q, want DRAINING", roots.folders[1].DrainState)
	}
	if roots.folders[1].DrainJobID == nil || *roots.folders[1].DrainJobID != result.MoveJobID {
		t.Errorf("drain job id = %v, want %d", roots.folders[1].DrainJobID, result.MoveJobID)
	}
	for _, a := range roots.attachments {
		if a.RootFolderID == 1 && a.AcceptsNew {
			t.Error("source root still accepts new placements after a committed drain")
		}
	}
}

func TestStart_ErrorsWhenTheRootFolderIsUnknown(t *testing.T) {
	cfg := drainjob.Config{
		Roots: &fakeRoots{folders: map[int64]settings.RootFolder{}}, Files: fakeFiles{},
		MoveJobs: &fakeMoveJobs{}, Queue: newTestQueue(t), Clock: clock.NewFakeAtEpoch(),
	}
	if _, err := drainjob.Start(t.Context(), cfg, 99, false); !errors.Is(err, drainjob.ErrRootFolderNotFound) {
		t.Errorf("Start = %v, want ErrRootFolderNotFound", err)
	}
}

func TestStart_ErrorsWhenTheRootHasNoFiles(t *testing.T) {
	cfg := drainjob.Config{
		Roots: &fakeRoots{folders: map[int64]settings.RootFolder{1: onlineRoot(1, 0)}},
		Files: fakeFiles{}, MoveJobs: &fakeMoveJobs{}, Queue: newTestQueue(t), Clock: clock.NewFakeAtEpoch(),
	}
	if _, err := drainjob.Start(t.Context(), cfg, 1, false); !errors.Is(err, drainjob.ErrNoFiles) {
		t.Errorf("Start = %v, want ErrNoFiles", err)
	}
}

func TestCompleter_AdvancesToDrainedWhenTheRootIsEmpty(t *testing.T) {
	roots := &fakeRoots{folders: map[int64]settings.RootFolder{
		1: {ID: 1, DrainState: settings.DrainStateDraining, DrainJobID: i64(7)},
	}}
	completer := drainjob.Completer{Roots: roots, Files: fakeFiles{}}

	if err := completer.CompleteDrainIfEmpty(t.Context(), 1, 7); err != nil {
		t.Fatalf("CompleteDrainIfEmpty: %v", err)
	}
	if roots.folders[1].DrainState != settings.DrainStateDrained || roots.folders[1].DrainJobID != nil {
		t.Errorf("root = %+v, want DRAINED/nil", roots.folders[1])
	}
}

func TestCompleter_LeavesDrainingWhenFilesRemain(t *testing.T) {
	roots := &fakeRoots{folders: map[int64]settings.RootFolder{
		1: {ID: 1, DrainState: settings.DrainStateDraining, DrainJobID: i64(7)},
	}}
	files := fakeFiles{1: {{MediaObjectID: 1, RelPath: "still/here.flac", SizeBytes: 1}}}
	completer := drainjob.Completer{Roots: roots, Files: files}

	if err := completer.CompleteDrainIfEmpty(t.Context(), 1, 7); err != nil {
		t.Fatalf("CompleteDrainIfEmpty: %v", err)
	}
	if roots.folders[1].DrainState != settings.DrainStateDraining {
		t.Errorf("DrainState = %q, want DRAINING to stay put while files remain", roots.folders[1].DrainState)
	}
}

// A stale move job id (a later drain already replaced this one, or StopDrain
// already reset the state) must not advance a state that is no longer this
// job's own to complete.
func TestCompleter_IgnoresAMismatchedMoveJobID(t *testing.T) {
	roots := &fakeRoots{folders: map[int64]settings.RootFolder{
		1: {ID: 1, DrainState: settings.DrainStateDraining, DrainJobID: i64(99)},
	}}
	completer := drainjob.Completer{Roots: roots, Files: fakeFiles{}}

	if err := completer.CompleteDrainIfEmpty(t.Context(), 1, 7); err != nil {
		t.Fatalf("CompleteDrainIfEmpty: %v", err)
	}
	if roots.folders[1].DrainState != settings.DrainStateDraining {
		t.Errorf("DrainState = %q, want untouched for a move job id that is not this root's own current drain",
			roots.folders[1].DrainState)
	}
}

func TestCompleter_IgnoresARootNotCurrentlyDraining(t *testing.T) {
	roots := &fakeRoots{folders: map[int64]settings.RootFolder{
		1: {ID: 1, DrainState: settings.DrainStateNone},
	}}
	completer := drainjob.Completer{Roots: roots, Files: fakeFiles{}}

	if err := completer.CompleteDrainIfEmpty(t.Context(), 1, 7); err != nil {
		t.Fatalf("CompleteDrainIfEmpty: %v", err)
	}
	if roots.folders[1].DrainState != settings.DrainStateNone {
		t.Errorf("DrainState = %q, want untouched", roots.folders[1].DrainState)
	}
}

// A destination attached to a different category from the source is never
// offered to placement.Resolve at all, so a drain with no real sibling
// refuses cleanly rather than raiding an unrelated category's own root.
func TestStart_RefusesWhenNoDestinationSharesACategory(t *testing.T) {
	roots := &fakeRoots{
		folders: map[int64]settings.RootFolder{1: onlineRoot(1, 0), 2: onlineRoot(2, 1000)},
		attachments: []settings.CategoryRoot{
			{CategoryID: 10, RootFolderID: 1, AcceptsNew: true},
			{CategoryID: 20, RootFolderID: 2, AcceptsNew: true},
		},
	}
	files := fakeFiles{1: {{MediaObjectID: 1, RelPath: "Artist/track.flac", SizeBytes: 100}}}
	cfg := drainjob.Config{
		Roots: roots, Files: files, MoveJobs: &fakeMoveJobs{}, Queue: newTestQueue(t), Clock: clock.NewFakeAtEpoch(),
	}

	result, err := drainjob.Start(t.Context(), cfg, 1, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Accepted || len(result.Plan.Moves) != 0 || len(result.Plan.Refused) != 1 {
		t.Fatalf("result = %+v, want a clean refusal, nothing accepted", result)
	}
}
