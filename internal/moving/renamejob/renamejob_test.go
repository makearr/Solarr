package renamejob_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

type roots map[int64]settings.RootFolder

func (r roots) RootFolder(id int64) (settings.RootFolder, bool) {
	root, ok := r[id]
	return root, ok
}

// noJobs fails the test if anything is persisted.
type noJobs struct{ t *testing.T }

func (n noJobs) Create(context.Context, library.NewMoveJob) (int64, error) {
	n.t.Fatal("a preview created a move job")
	return 0, nil
}

func (n noJobs) AddItems(context.Context, int64, []library.NewMoveItem) error {
	n.t.Fatal("a preview added move items")
	return nil
}

// A preview on a root mounted read only still names every file, with the refusal beside it (user, 2026-10-23).
func TestStart_PreviewNamesFilesOnARootItMayNotChange(t *testing.T) {
	cfg := renamejob.Config{
		Roots:    roots{1: {ID: 1, Online: true, Writable: false}},
		MoveJobs: noJobs{t},
	}
	targets := []renamejob.Target{{MediaObjectID: 7, WorkID: 3, SizeBytes: 10, CurrentRelPath: "a/old.flac", ProposedRelPath: "a/new.flac"}}

	plan, jobID, _, err := renamejob.Start(t.Context(), cfg, 1, 0, targets, true)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantMoves := []mover.PlannedMove{{
		MediaObjectID: 7, SourceRootFolderID: 1, SourceRelPath: "a/old.flac", DestRootFolderID: 1,
		DestRelPath: "a/new.flac", SizeBytes: 10, AffinityKey: "work:3",
	}}
	wantRefused := []mover.RefusedFile{{MediaObjectID: 7, AffinityKey: "work:3", Reason: "destination root 1 is not writable"}}
	if jobID != 0 || !reflect.DeepEqual(plan.Moves, wantMoves) || !reflect.DeepEqual(plan.Refused, wantRefused) {
		t.Errorf("preview = job %d, moves %+v, refused %+v; want no job, the rename named and refused", jobID, plan.Moves, plan.Refused)
	}
}
