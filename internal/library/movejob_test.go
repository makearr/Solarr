package library_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
)

func TestMoveJobs_CreateAndGet(t *testing.T) {
	opened, _ := newLibrary(t)
	jobs := library.NewMoveJobs(opened)
	ctx := t.Context()

	id, err := jobs.Create(ctx, library.NewMoveJob{
		Reason: "DRAIN", FilesTotal: 2, BytesTotal: 300,
		PlanJSON: "{}", CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != library.MoveJobPlanned || got.FilesTotal != 2 || got.BytesTotal != 300 {
		t.Errorf("job = %+v, want PLANNED/2/300", got)
	}
}

func TestMoveJobs_AddItemsAndWorkThroughThem(t *testing.T) {
	opened, _ := newLibrary(t)
	jobs := library.NewMoveJobs(opened)
	ctx := t.Context()

	id, err := jobs.Create(ctx, library.NewMoveJob{
		Reason: "REBALANCE", FilesTotal: 2, BytesTotal: 300, PlanJSON: "{}", CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := jobs.AddItems(ctx, id, []library.NewMoveItem{
		{MediaObjectID: 1, AffinityKey: "a", DestRootID: 2, DestRelPath: "New/1.flac", Bytes: 100},
		{MediaObjectID: 2, AffinityKey: "a", DestRootID: 2, DestRelPath: "New/2.flac", Bytes: 200},
	}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}

	if err := jobs.SetRunning(ctx, id); err != nil {
		t.Fatalf("SetRunning: %v", err)
	}
	got, err := jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != library.MoveJobRunning {
		t.Errorf("State = %q, want RUNNING", got.State)
	}

	first, err := jobs.NextPendingItem(ctx, id)
	if err != nil {
		t.Fatalf("NextPendingItem: %v", err)
	}
	if first.MediaObjectID != 1 || first.State != library.MoveItemPending {
		t.Fatalf("first = %+v, want media object 1, PENDING", first)
	}
	if err := jobs.SetItemState(ctx, id, first.MediaObjectID, library.MoveItemDone, ""); err != nil {
		t.Fatalf("SetItemState: %v", err)
	}

	second, err := jobs.NextPendingItem(ctx, id)
	if err != nil {
		t.Fatalf("NextPendingItem: %v", err)
	}
	if second.MediaObjectID != 2 {
		t.Fatalf("second = %+v, want media object 2 (1 is already DONE)", second)
	}
	if err := jobs.SetItemState(ctx, id, second.MediaObjectID, library.MoveItemDone, ""); err != nil {
		t.Fatalf("SetItemState: %v", err)
	}

	got, err = jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FilesDone != 2 || got.BytesDone != 300 {
		t.Errorf("progress = %+v, want files_done=2 bytes_done=300", got)
	}

	if _, err := jobs.NextPendingItem(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("NextPendingItem after every item is DONE = %v, want sql.ErrNoRows", err)
	}

	if err := jobs.Finish(ctx, id, library.MoveJobDone, 200, ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	done, err := jobs.List(ctx, library.MoveJobDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(done) != 1 || done[0].ID != id {
		t.Fatalf("done sessions = %+v, want just this one", done)
	}
}

func TestMoveJobs_SetItemStateRecordsAnError(t *testing.T) {
	opened, _ := newLibrary(t)
	jobs := library.NewMoveJobs(opened)
	ctx := t.Context()

	id, err := jobs.Create(ctx, library.NewMoveJob{
		Reason: "RENAME", FilesTotal: 1, BytesTotal: 100, PlanJSON: "{}", CreatedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := jobs.AddItems(ctx, id, []library.NewMoveItem{
		{MediaObjectID: 1, DestRootID: 2, DestRelPath: "New/1.flac", Bytes: 100},
	}); err != nil {
		t.Fatalf("AddItems: %v", err)
	}
	if err := jobs.SetItemState(ctx, id, 1, library.MoveItemFailed, "disk full"); err != nil {
		t.Fatalf("SetItemState: %v", err)
	}

	items, err := jobs.ListItems(ctx, id)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].State != library.MoveItemFailed || items[0].Error != "disk full" {
		t.Fatalf("items = %+v, want one FAILED item with the error recorded", items)
	}
}
