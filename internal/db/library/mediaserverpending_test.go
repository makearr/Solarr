package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestMediaServerPending_AddIgnoresADuplicateAndDueHonoursTheCutoff(t *testing.T) {
	opened, _ := newLibrary(t)
	queue := library.NewMediaServerPending(opened)
	row := library.PendingPush{ServerID: 1, CategoryID: 2, Path: "/data/a", Change: library.PendingCreated, WorkID: 3, QueuedAt: 100}
	later := library.PendingPush{ServerID: 1, CategoryID: 2, Path: "/data/b", Change: library.PendingDeleted, QueuedAt: 500}
	if err := queue.Add(t.Context(), []library.PendingPush{row, row, later}); err != nil {
		t.Fatal(err)
	}
	due, err := queue.Due(t.Context(), 1, 200, 10)
	if err != nil {
		t.Fatal(err)
	}
	row.ID = due[0].ID
	if !reflect.DeepEqual(due, []library.PendingPush{row}) {
		t.Fatalf("due = %+v", due)
	}
	if err := queue.Bump(t.Context(), []int64{row.ID}); err != nil {
		t.Fatal(err)
	}
	due, _ = queue.Due(t.Context(), 1, 200, 10)
	if due[0].Attempts != 1 {
		t.Fatalf("attempts = %d", due[0].Attempts)
	}
	servers, _ := queue.ServersDue(t.Context(), 200)
	if !reflect.DeepEqual(servers, []int64{1}) {
		t.Fatalf("servers = %v", servers)
	}
	if err := queue.Delete(t.Context(), []int64{row.ID}); err != nil {
		t.Fatal(err)
	}
	if due, _ = queue.Due(t.Context(), 1, 200, 10); len(due) != 0 {
		t.Fatalf("due after delete = %+v", due)
	}
}
