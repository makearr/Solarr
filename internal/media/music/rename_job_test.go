package music_test

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/moving/moverjob"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/migrations"
)

type fakeRenameRoots map[int64]settings.RootFolder

func (r fakeRenameRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	root, ok := r[id]
	return root, ok
}

type fakeRenameFiles map[int64][]library.FileOnRoot

func (f fakeRenameFiles) PathsForRoot(_ context.Context, rootFolderID int64) ([]library.FileOnRoot, error) {
	return f[rootFolderID], nil
}

type fakeRenameIdentities map[int64]library.FileIdentity

func (f fakeRenameIdentities) IdentityForFile(_ context.Context, mediaObjectID int64) (library.FileIdentity, bool, error) {
	identity, found := f[mediaObjectID]
	return identity, found, nil
}

type fakeRenameWorks map[int64]library.WorkDetail

func (f fakeRenameWorks) Detail(_ context.Context, workID int64) (library.WorkDetail, error) {
	return f[workID], nil
}

func renameWorkDetailFixture() library.WorkDetail {
	return library.WorkDetail{
		Work: librarydb.Work{ID: 1, CanonicalTitle: "Tender Prey", Year: sql.NullInt64{Int64: 1988, Valid: true}},
		Parts: []library.PartDetail{
			{Part: librarydb.WorkPart{ID: 100, WorkID: 1, MediaRef: "disc1:track1", Title: sql.NullString{String: "The Furab Keke", Valid: true}}},
			{Part: librarydb.WorkPart{ID: 101, WorkID: 1, MediaRef: "disc1:track2", Title: sql.NullString{String: "Up Jumped the Devil", Valid: true}}},
		},
		Credits: []library.Credit{{PersonName: "Nick Cave and the Bad Seeds", Role: music.CreditRoleArtist}},
	}
}

func newRenameQueue(t *testing.T) *jobs.Queue {
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

type fakeRenameMoveJobs struct {
	nextID int64
	items  []library.NewMoveItem
}

func (m *fakeRenameMoveJobs) Create(_ context.Context, _ library.NewMoveJob) (int64, error) {
	m.nextID = 1
	return m.nextID, nil
}

func (m *fakeRenameMoveJobs) AddItems(_ context.Context, _ int64, items []library.NewMoveItem) error {
	m.items = items
	return nil
}

func TestRenamePreview_ListsChangedFilesOnly(t *testing.T) {
	cfg := music.RenameConfig{
		Roots: fakeRenameRoots{},
		Files: fakeRenameFiles{1: {
			{MediaObjectID: 1, RelPath: "Old/01.flac", SizeBytes: 100},
			{MediaObjectID: 2, RelPath: "Nick Cave and the Bad Seeds/Tender Prey (1988)/Nick Cave and the Bad Seeds - 02 - Up Jumped the Devil.flac", SizeBytes: 100},
		}},
		Identities: fakeRenameIdentities{
			1: {WorkID: 1, MediaRef: "disc1:track1"},
			2: {WorkID: 1, MediaRef: "disc1:track2"},
		},
		Works: fakeRenameWorks{1: renameWorkDetailFixture()},
	}

	targets, err := music.RenamePreview(t.Context(), cfg, 1)
	if err != nil {
		t.Fatalf("RenamePreview: %v", err)
	}
	if len(targets) != 1 || targets[0].MediaObjectID != 1 {
		t.Fatalf("targets = %+v, want only media object 1 (media object 2 is already correct)", targets)
	}
}

func TestRenamePreview_SkipsFilesWithNoIdentity(t *testing.T) {
	cfg := music.RenameConfig{
		Roots:      fakeRenameRoots{},
		Files:      fakeRenameFiles{1: {{MediaObjectID: 9, RelPath: "unaligned.flac", SizeBytes: 100}}},
		Identities: fakeRenameIdentities{},
		Works:      fakeRenameWorks{},
	}

	targets, err := music.RenamePreview(t.Context(), cfg, 1)
	if err != nil {
		t.Fatalf("RenamePreview: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("targets = %+v, want none for an unaligned file", targets)
	}
}

func TestRenameStart_PersistsAndEnqueuesWhenSomethingChanged(t *testing.T) {
	moveJobs := &fakeRenameMoveJobs{}
	cfg := music.RenameConfig{
		Roots: fakeRenameRoots{1: {ID: 1, Online: true, Writable: true, FSID: "fs-a"}},
		Files: fakeRenameFiles{1: {
			{MediaObjectID: 1, RelPath: "Old/01.flac", SizeBytes: 100},
		}},
		Identities: fakeRenameIdentities{1: {WorkID: 1, MediaRef: "disc1:track1"}},
		Works:      fakeRenameWorks{1: renameWorkDetailFixture()},
		MoveJobs:   moveJobs,
		Queue:      newRenameQueue(t),
		Clock:      clock.NewFakeAtEpoch(),
	}

	plan, jobID, err := music.RenameStart(t.Context(), cfg, 1, false)
	if err != nil {
		t.Fatalf("RenameCommit: %v", err)
	}
	if jobID == 0 {
		t.Fatal("jobID = 0, want a real move job id")
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("plan.Moves = %+v, want one move", plan.Moves)
	}
	if len(moveJobs.items) != 1 || moveJobs.items[0].AffinityKey != "work:1" {
		t.Errorf("items = %+v, want one item grouped under work:1", moveJobs.items)
	}
}

// preview:true computes the real plan (guards included -- unlike the old
// two-entry-point design, this now actually runs mover.Plan) but persists
// nothing.
func TestRenameStart_PreviewComputesThePlanWithoutPersistingAnything(t *testing.T) {
	moveJobs := &fakeRenameMoveJobs{}
	cfg := music.RenameConfig{
		Roots: fakeRenameRoots{1: {ID: 1, Online: true, Writable: true, FSID: "fs-a"}},
		Files: fakeRenameFiles{1: {
			{MediaObjectID: 1, RelPath: "Old/01.flac", SizeBytes: 100},
		}},
		Identities: fakeRenameIdentities{1: {WorkID: 1, MediaRef: "disc1:track1"}},
		Works:      fakeRenameWorks{1: renameWorkDetailFixture()},
		MoveJobs:   moveJobs,
		Queue:      newRenameQueue(t),
		Clock:      clock.NewFakeAtEpoch(),
	}

	plan, jobID, err := music.RenameStart(t.Context(), cfg, 1, true)
	if err != nil {
		t.Fatalf("RenameStart: %v", err)
	}
	if jobID != 0 {
		t.Errorf("jobID = %d, want 0 for a preview", jobID)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("plan.Moves = %+v, want one computed move", plan.Moves)
	}
	if moveJobs.items != nil {
		t.Error("MoveJobs.AddItems was called during a preview")
	}
}

func TestRenameStart_DoesNothingWhenNothingChanged(t *testing.T) {
	moveJobs := &fakeRenameMoveJobs{}
	cfg := music.RenameConfig{
		Roots: fakeRenameRoots{1: {ID: 1, Online: true, Writable: true, FSID: "fs-a"}},
		Files: fakeRenameFiles{1: {
			{MediaObjectID: 1, RelPath: "Nick Cave and the Bad Seeds/Tender Prey (1988)/Nick Cave and the Bad Seeds - 01 - The Furab Keke.flac", SizeBytes: 100},
		}},
		Identities: fakeRenameIdentities{1: {WorkID: 1, MediaRef: "disc1:track1"}},
		Works:      fakeRenameWorks{1: renameWorkDetailFixture()},
		MoveJobs:   moveJobs,
		Queue:      newRenameQueue(t),
		Clock:      clock.NewFakeAtEpoch(),
	}

	plan, jobID, err := music.RenameStart(t.Context(), cfg, 1, false)
	if err != nil {
		t.Fatalf("RenameCommit: %v", err)
	}
	if jobID != 0 {
		t.Errorf("jobID = %d, want 0 when nothing changed", jobID)
	}
	if len(plan.Moves) != 0 {
		t.Errorf("plan.Moves = %+v, want none", plan.Moves)
	}
	if moveJobs.items != nil {
		t.Error("MoveJobs.AddItems was called despite nothing to rename")
	}
}

// Nothing to rename short-circuits before ever looking up the root folder's
// own policy -- an unknown root id must not turn "there was nothing to do
// anyway" into a spurious error.
func TestRenameStart_DoesNothingForAnUnknownRootWithNothingToRename(t *testing.T) {
	cfg := music.RenameConfig{
		Roots: fakeRenameRoots{},
		Files: fakeRenameFiles{99: {
			{MediaObjectID: 1, RelPath: "Nick Cave and the Bad Seeds/Tender Prey (1988)/Nick Cave and the Bad Seeds - 01 - The Furab Keke.flac", SizeBytes: 100},
		}},
		Identities: fakeRenameIdentities{1: {WorkID: 1, MediaRef: "disc1:track1"}},
		Works:      fakeRenameWorks{1: renameWorkDetailFixture()},
	}

	plan, jobID, err := music.RenameStart(t.Context(), cfg, 99, false)
	if err != nil {
		t.Fatalf("RenameCommit: %v", err)
	}
	if jobID != 0 || len(plan.Moves) != 0 {
		t.Errorf("plan/jobID = %+v/%d, want nothing committed and no error for an unknown root with nothing to rename", plan, jobID)
	}
}

func TestRenameStart_RefusesOnAReadOnlyRootWithoutAllowRename(t *testing.T) {
	moveJobs := &fakeRenameMoveJobs{}
	cfg := music.RenameConfig{
		Roots: fakeRenameRoots{1: {ID: 1, Online: true, Writable: true, FSID: "fs-a", ReadOnly: true}},
		Files: fakeRenameFiles{1: {
			{MediaObjectID: 1, RelPath: "Old/01.flac", SizeBytes: 100},
		}},
		Identities: fakeRenameIdentities{1: {WorkID: 1, MediaRef: "disc1:track1"}},
		Works:      fakeRenameWorks{1: renameWorkDetailFixture()},
		MoveJobs:   moveJobs,
		Queue:      newRenameQueue(t),
		Clock:      clock.NewFakeAtEpoch(),
	}

	plan, jobID, err := music.RenameStart(t.Context(), cfg, 1, false)
	if err != nil {
		t.Fatalf("RenameCommit: %v", err)
	}
	if jobID != 0 || len(plan.Moves) != 0 || len(plan.Refused) != 1 {
		t.Fatalf("plan/jobID = %+v/%d, want refused and nothing committed", plan, jobID)
	}
}
