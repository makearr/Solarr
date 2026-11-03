package renamejob_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

type moveJobs map[string][]library.Job

func (m moveJobs) List(_ context.Context, state string) ([]library.Job, error) { return m[state], nil }

// A library whose last rename is still planned, running or paused waits; the others are renamed.
func TestAutomaticJob_SkipsALibraryStillRenaming(t *testing.T) {
	busy, other := int64(2), int64(9)
	var started []int64
	job := renamejob.NewAutomaticJob(renamejob.AutomaticConfig{
		Libraries: func() []int64 { return []int64{1, 2, 3} },
		Rename:    func(_ context.Context, id int64) error { started = append(started, id); return nil },
		MoveJobs: moveJobs{
			library.MoveJobRunning: {{Reason: string(mover.ReasonRename), CategoryID: &busy}},
			library.MoveJobPaused:  {{Reason: string(mover.ReasonCategory), CategoryID: &other}, {Reason: string(mover.ReasonRename)}},
		},
	})
	if err := job.Run(t.Context(), jobs.Job{Type: renamejob.AutomaticJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []int64{1, 3}; !reflect.DeepEqual(started, want) {
		t.Errorf("renamed libraries = %v, want %v", started, want)
	}
}
