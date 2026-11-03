package jobs_test

import (
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/jobs"
)

func finishOne(t *testing.T, queue *jobs.Queue, request jobs.Request) jobs.Job {
	t.Helper()
	mustEnqueue(t, queue, request)
	claimed := claimOne(t, queue, jobs.PoolMaintenance, "worker-1")
	if err := queue.Complete(t.Context(), claimed, "worker-1", startJob(t, queue, claimed, "worker-1")); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return claimed
}

func TestPruneFinished_DeletesOnlyOldFinishedJobsNothingDependsOn(t *testing.T) {
	queue, fake := newQueue(t)
	old := finishOne(t, queue, jobs.Request{Type: typeBackup})
	parent := finishOne(t, queue, jobs.Request{Type: typeBackup})
	waiting := mustEnqueue(t, queue, jobs.Request{Type: typeImport, DependsOn: parent.ID})
	fake.Advance(30 * 24 * time.Hour)
	recent := finishOne(t, queue, jobs.Request{Type: typeBackup})

	deleted, err := queue.PruneFinished(t.Context(), fake.Now().Add(-14*24*time.Hour), 200)
	if err != nil {
		t.Fatalf("PruneFinished: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if _, err := queue.Get(t.Context(), old.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("old finished job: Get = %v, want ErrNotFound", err)
	}
	for name, id := range map[string]int64{"depended on": parent.ID, "pending": waiting.ID, "recent": recent.ID} {
		if _, err := queue.Get(t.Context(), id); err != nil {
			t.Errorf("%s job %d: Get = %v, want it kept", name, id, err)
		}
	}
}

func TestPruneFinished_StopsAtTheBatchSize(t *testing.T) {
	queue, fake := newQueue(t)
	finishOne(t, queue, jobs.Request{Type: typeBackup})
	finishOne(t, queue, jobs.Request{Type: typeBackup})
	fake.Advance(time.Hour)

	for _, want := range []int64{1, 1, 0} {
		deleted, err := queue.PruneFinished(t.Context(), fake.Now(), 1)
		if err != nil {
			t.Fatalf("PruneFinished: %v", err)
		}
		if deleted != want {
			t.Errorf("deleted = %d, want %d", deleted, want)
		}
	}
}
