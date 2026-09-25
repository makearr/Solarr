package jobs_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/jobsdb"
	"github.com/makearr/Solarr/internal/jobs"
)

func statesOf(t *testing.T, queue *jobs.Queue, ids ...int64) []jobs.State {
	t.Helper()
	states := make([]jobs.State, len(ids))
	for index, id := range ids {
		job, err := queue.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get %d: %v", id, err)
		}
		states[index] = job.State
	}
	return states
}

func TestFailUnfinished_LeavesJobsThatAlreadyFinished(t *testing.T) {
	queue, _, _ := newQueueWithStore(t)
	pending := mustEnqueue(t, queue, jobs.Request{Type: typeImport})
	cancelled := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	if err := queue.Cancel(t.Context(), cancelled.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	failed, err := queue.FailUnfinished(t.Context(), "restored from backup")
	if err != nil {
		t.Fatalf("FailUnfinished: %v", err)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	got := statesOf(t, queue, pending.ID, cancelled.ID)
	if got[0] != jobs.StateFailed || got[1] != jobs.StateCancelled {
		t.Errorf("states = %v, want [FAILED CANCELLED]", got)
	}
}

// The reason lands in job.last_error, which is where a user sees why their queue
// emptied. An unexplained failure is the thing this exists to avoid.
func TestFailUnfinished_RecordsTheReasonOnEachJob(t *testing.T) {
	queue, _, _ := newQueueWithStore(t)
	job := mustEnqueue(t, queue, jobs.Request{Type: typeImport})

	const reason = "restored from the 03:00 backup"
	if _, err := queue.FailUnfinished(t.Context(), reason); err != nil {
		t.Fatalf("FailUnfinished: %v", err)
	}
	found, err := queue.Get(t.Context(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found.LastError != reason {
		t.Errorf("LastError = %q, want %q", found.LastError, reason)
	}
}

func TestFailUnfinished_CrossesTheBatchBoundary(t *testing.T) {
	queue, _, _ := newQueueWithStore(t)
	const total = 205
	for index := range total {
		mustEnqueue(t, queue, jobs.Request{
			Type: typeBackup, ActionKey: fmt.Sprintf("backup-%d", index),
		})
	}

	failed, err := queue.FailUnfinished(t.Context(), "restored from backup")
	if err != nil {
		t.Fatalf("FailUnfinished: %v", err)
	}
	if failed != total {
		t.Errorf("failed = %d, want %d", failed, total)
	}
}

func TestFailUnfinished_OnAnEmptyQueueDoesNothing(t *testing.T) {
	queue, _, _ := newQueueWithStore(t)
	failed, err := queue.FailUnfinished(t.Context(), "restored from backup")
	if err != nil {
		t.Fatalf("FailUnfinished: %v", err)
	}
	if failed != 0 {
		t.Errorf("failed = %d on an empty queue, want 0", failed)
	}
}

func TestHistoryAppend_StoresTheEntryWithItsPayload(t *testing.T) {
	_, opened, fake := newQueueWithStore(t)
	history := jobs.NewHistory(opened, fake)

	id, err := history.Append(t.Context(), jobs.Event{
		Kind: "database.restored", SubjectType: "database", Data: `{"backup":"one"}`,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	found := listByKind(t, opened, "database.restored")
	if len(found) != 1 || found[0].ID != id {
		t.Fatalf("found %+v, want one entry with id %d", found, id)
	}
	if found[0].DataJson != `{"backup":"one"}` {
		t.Errorf("DataJson = %q, want the payload back", found[0].DataJson)
	}
	// An absent subject id must read back as absent, not as row zero.
	if found[0].SubjectID.Valid {
		t.Errorf("SubjectID = %v, want NULL", found[0].SubjectID)
	}
	if found[0].At != fake.Now().Unix() {
		t.Errorf("At = %d, want the injected clock's %d", found[0].At, fake.Now().Unix())
	}
}

func listByKind(t *testing.T, opened *store.Store, kind string) []jobsdb.EventLog {
	t.Helper()
	found, err := jobsdb.New(opened.Read()).ListEventsByKind(t.Context(), jobsdb.ListEventsByKindParams{
		Kind: kind, BeforeID: 0, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListEventsByKind: %v", err)
	}
	return found
}

// History lists the asked-for kinds newest first, for one subject or all, and pages by id.
func TestHistory_ListsKindsForASubjectNewestFirst(t *testing.T) {
	_, opened, fake := newQueueWithStore(t)
	history := jobs.NewHistory(opened, fake)
	for _, event := range []jobs.Event{
		{Kind: "download.grabbed", SubjectType: "work", SubjectID: 1, Data: "{}"},
		{Kind: "recovery", Data: "{}"},
		{Kind: "download.grabbed", SubjectType: "work", SubjectID: 2, Data: "{}"},
		{Kind: "download.imported", SubjectType: "work", SubjectID: 1, Data: "{}"},
	} {
		if _, err := history.Append(t.Context(), event); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	kinds := []string{"download.grabbed", "download.imported"}
	summarise := func(events []jobs.StoredEvent) []string {
		var got []string
		for _, event := range events {
			got = append(got, fmt.Sprintf("%d %s %s:%d", event.ID, event.Kind, event.SubjectType, event.SubjectID))
		}
		return got
	}

	all, err := history.List(t.Context(), jobs.HistoryQuery{Kinds: kinds, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := summarise(all), []string{
		"4 download.imported work:1", "3 download.grabbed work:2", "1 download.grabbed work:1",
	}; !slices.Equal(got, want) {
		t.Errorf("all = %q, want %q", got, want)
	}
	album, err := history.List(t.Context(), jobs.HistoryQuery{Kinds: kinds, SubjectType: "work", SubjectID: 1, BeforeID: 4, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := summarise(album), []string{"1 download.grabbed work:1"}; !slices.Equal(got, want) {
		t.Errorf("album before 4 = %q, want %q", got, want)
	}
}
