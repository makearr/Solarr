package drainjob_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/drainjob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// fakeWorkFiles is one work's files per root.
type fakeWorkFiles map[int64][]library.FileOnRoot

func (f fakeWorkFiles) PathsForWorkOnRoot(_ context.Context, rootFolderID, _ int64) ([]library.FileOnRoot, error) {
	return f[rootFolderID], nil
}

// A series spread over two roots of its library comes together on the chosen one, its paths kept; another
// library's root is never read, and no root's own state changes (Sonarr's Edit -> root folder).
func TestStartWorkMove_BringsTheWorksFilesOntoTheChosenRoot(t *testing.T) {
	roots := &fakeRoots{
		folders: map[int64]settings.RootFolder{1: onlineRoot(1, 1000), 2: onlineRoot(2, 1000), 3: onlineRoot(3, 1000), 9: onlineRoot(9, 1000)},
		attachments: []settings.CategoryRoot{
			{CategoryID: 10, RootFolderID: 1, AcceptsNew: true}, {CategoryID: 10, RootFolderID: 2, AcceptsNew: true},
			{CategoryID: 10, RootFolderID: 3, AcceptsNew: true}, {CategoryID: 20, RootFolderID: 9, AcceptsNew: true},
		},
	}
	files := fakeWorkFiles{
		1: {{MediaObjectID: 5, RelPath: "Show/Season 1/e1.mkv", SizeBytes: 10}},
		2: {{MediaObjectID: 6, RelPath: "Show/Season 2/e1.mkv", SizeBytes: 20}},
		3: {{MediaObjectID: 7, RelPath: "Show/Season 3/e1.mkv", SizeBytes: 30}},
		9: {{MediaObjectID: 8, RelPath: "Other/file.mkv", SizeBytes: 40}},
	}
	moveJobs := &fakeMoveJobs{}
	cfg := drainjob.Config{Roots: roots, MoveJobs: moveJobs, Queue: newTestQueue(t), Clock: clock.NewFakeAtEpoch()}

	result, err := drainjob.StartWorkMove(t.Context(), cfg, files, 42, 3, false)
	if err != nil {
		t.Fatalf("StartWorkMove: %v", err)
	}
	if !result.Accepted || result.MoveJobID != 1 {
		t.Fatalf("result = %+v, want an accepted move job", result)
	}
	want := fakeMoveJob{Reason: "MOVE", FilesTotal: 2, BytesTotal: 30, Items: []library.NewMoveItem{
		{MediaObjectID: 5, AffinityKey: "Show", DestRootID: 3, DestRelPath: "Show/Season 1/e1.mkv", Bytes: 10},
		{MediaObjectID: 6, AffinityKey: "Show", DestRootID: 3, DestRelPath: "Show/Season 2/e1.mkv", Bytes: 20},
	}}
	if !reflect.DeepEqual(moveJobs.created, want) {
		t.Errorf("move job = %+v\nwant %+v", moveJobs.created, want)
	}
	for _, root := range []int64{1, 2, 3} {
		if roots.folders[root].DrainState != "" || !roots.attachments[root-1].AcceptsNew {
			t.Errorf("root %d changed state: %+v", root, roots.folders[root])
		}
	}

	if _, err := drainjob.StartWorkMove(t.Context(), cfg, fakeWorkFiles{3: files[3]}, 42, 3, false); !errors.Is(err, drainjob.ErrNothingToMove) {
		t.Errorf("already on the root: err = %v, want ErrNothingToMove", err)
	}
}

// A move that does not fit is answered and never started: half a series on each root is worse than none moved.
func TestStartWorkMove_AMoveThatDoesNotFitStartsNothing(t *testing.T) {
	roots := &fakeRoots{
		folders: map[int64]settings.RootFolder{1: onlineRoot(1, 1000), 2: onlineRoot(2, 5)},
		attachments: []settings.CategoryRoot{
			{CategoryID: 10, RootFolderID: 1, AcceptsNew: true}, {CategoryID: 10, RootFolderID: 2, AcceptsNew: true},
		},
	}
	moveJobs := &fakeMoveJobs{}
	cfg := drainjob.Config{Roots: roots, MoveJobs: moveJobs, Queue: newTestQueue(t), Clock: clock.NewFakeAtEpoch()}
	result, err := drainjob.StartWorkMove(t.Context(), cfg,
		fakeWorkFiles{1: {{MediaObjectID: 5, RelPath: "Show/e1.mkv", SizeBytes: 10}}}, 42, 2, false)
	if err != nil {
		t.Fatalf("StartWorkMove: %v", err)
	}
	if result.Accepted || len(result.Plan.Refused) == 0 || moveJobs.nextID != 0 {
		t.Errorf("result = %+v, created = %+v: want a refused plan and no move job", result, moveJobs.created)
	}
}
