package music_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func newSearchQueue(t *testing.T) *jobs.Queue {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{Name: "jobs", Path: filepath.Join(directory, "jobs.db"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(directory, "backups"), owner); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry()
	registry.MustRegister(music.SearchCategoryDefinition)
	return jobs.NewQueue(opened, fake, registry, jobs.NoJitter)
}

// A search a person asks for runs ahead of the background hunt's, and folds into a queued hunt search by raising it (SCHED-8).
func TestEnqueueSearchTakesTheCallersPriority(t *testing.T) {
	queue := newSearchQueue(t)
	hunted, err := music.EnqueueSearchItems(t.Context(), queue, jobs.PriorityLow, 7, []int64{3, 1, 2})
	if err != nil || hunted.Job.Priority != jobs.PriorityLow {
		t.Fatalf("hunt enqueue = %+v, %v; want priority %d", hunted.Job, err, jobs.PriorityLow)
	}
	clicked, err := music.EnqueueSearchItems(t.Context(), queue, jobs.PriorityInteractive, 7, []int64{1, 2, 3})
	if err != nil || !clicked.Duplicate || clicked.Job.ID != hunted.Job.ID || clicked.Job.Priority != jobs.PriorityInteractive {
		t.Fatalf("click enqueue = %+v duplicate=%v, %v; want the queued hunt search raised to %d", clicked.Job, clicked.Duplicate, err, jobs.PriorityInteractive)
	}
	stored, err := queue.Get(t.Context(), hunted.Job.ID)
	if err != nil || stored.Priority != jobs.PriorityInteractive {
		t.Fatalf("stored job = %+v, %v; want the raised priority", stored, err)
	}
	category, err := music.EnqueueSearchCategory(t.Context(), queue, jobs.PriorityInteractive, 9)
	if err != nil || category.Job.Priority != jobs.PriorityInteractive {
		t.Errorf("category enqueue = %+v, %v; want interactive", category.Job, err)
	}
}

func TestEnqueueSearchKeyStaysSmallForABulkSearch(t *testing.T) {
	queue := newSearchQueue(t)
	ids := make([]int64, 20000)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	result, err := music.EnqueueSearchItems(t.Context(), queue, jobs.PriorityInteractive, 7, ids)
	if err != nil || len(result.Job.ActionKey) > 100 {
		t.Fatalf("action key of %d bytes, %v; want a short one", len(result.Job.ActionKey), err)
	}
}
