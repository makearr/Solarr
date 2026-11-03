package renamejob

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
)

// AutomaticJobType is the registered type of the sweep that renames libraries whose naming says automatic.
const AutomaticJobType = jobs.Type("library.rename_automatic")

// AutomaticScheduleKey is the schedule that runs the sweep.
const AutomaticScheduleKey = "library_rename_automatic"

// AutomaticActionKey deduplicates the sweep: one at a time (Invariant 16).
const AutomaticActionKey = "library.rename_automatic"

// AutomaticDefinition declares the sweep; the renames themselves are move jobs.
var AutomaticDefinition = jobs.Definition{
	Name: "Renaming libraries set to rename automatically",
	Type: AutomaticJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 1,
}

// MoveJobLister reads the move jobs; satisfied by library.MoveJobs.
type MoveJobLister interface {
	List(ctx context.Context, filterState string) ([]library.Job, error)
}

// AutomaticConfig is what an AutomaticJob needs; Logger is optional.
type AutomaticConfig struct {
	// Libraries lists the ids of the libraries whose naming says automatic, as they are now.
	Libraries func() []int64
	// Rename plans and starts one library's rename on every root.
	Rename   func(ctx context.Context, categoryID int64) error
	MoveJobs MoveJobLister
	Logger   *slog.Logger
}

// AutomaticJob starts the rename of each automatic library, the plan made as a manual one is; a library whose last
// rename is still unfinished waits for the next run instead of planning over files that are about to move.
type AutomaticJob struct {
	config AutomaticConfig
	logger *slog.Logger
}

// NewAutomaticJob builds the handler.
func NewAutomaticJob(config AutomaticConfig) *AutomaticJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AutomaticJob{config: config, logger: logger}
}

// Run starts the renames; one library failing does not stop the others.
func (j *AutomaticJob) Run(ctx context.Context, _ jobs.Job) error {
	busy, err := j.libraryIDsRenaming(ctx)
	if err != nil {
		return jobs.Retryablef("library.rename_automatic", string(AutomaticJobType), err)
	}
	for _, categoryID := range j.config.Libraries() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if busy[categoryID] {
			continue
		}
		if err := j.config.Rename(ctx, categoryID); err != nil {
			j.logger.Warn("automatic rename", slog.Int64("category_id", categoryID), slog.Any("error", err))
		}
	}
	return nil
}

// libraryIDsRenaming are the libraries with a rename job planned, running or paused.
func (j *AutomaticJob) libraryIDsRenaming(ctx context.Context) (map[int64]bool, error) {
	busy := map[int64]bool{}
	for _, state := range []string{library.MoveJobPlanned, library.MoveJobRunning, library.MoveJobPaused} {
		moveJobs, err := j.config.MoveJobs.List(ctx, state)
		if err != nil {
			return nil, err
		}
		for _, moveJob := range moveJobs {
			if moveJob.Reason == string(mover.ReasonRename) && moveJob.CategoryID != nil {
				busy[*moveJob.CategoryID] = true
			}
		}
	}
	return busy, nil
}

// EnqueueAutomatic submits one sweep, deduplicated by AutomaticActionKey.
func EnqueueAutomatic(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: AutomaticJobType, ActionKey: AutomaticActionKey, Priority: jobs.PriorityNormal})
}
