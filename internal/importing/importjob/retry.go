package importjob

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
)

// RetryJobType is the registered type of the sweep that places what a library could not take when it was imported.
const RetryJobType = jobs.Type("import.place_unplaced")

// RetryScheduleKey is the schedule that runs the sweep.
const RetryScheduleKey = "import_retry"

// RetryActionKey deduplicates the sweep: one at a time (Invariant 16).
const RetryActionKey = "import.place_unplaced"

// RetryDefinition declares the sweep: in the import pool, so it never places beside a running import (concurrency 1).
var RetryDefinition = jobs.Definition{
	Name: "Placing imports a library could not take",
	Type: RetryJobType, Pool: jobs.PoolImport, Mutating: true, MaxAttempts: 1,
}

// retryPageSize bounds one read of the sessions to retry.
const retryPageSize = 200

// RetryJob places the units an import decided but its library could not take (no root, no room, root offline), so
// the download the poller kept for them is imported once a root can (user, 2026-10-24).
type RetryJob struct {
	sessions  library.ImportSessions
	committer ManualCommitter
	logger    *slog.Logger
}

// NewRetryJob builds the handler; logger may be nil.
func NewRetryJob(sessions library.ImportSessions, committer ManualCommitter, logger *slog.Logger) *RetryJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &RetryJob{sessions: sessions, committer: committer, logger: logger}
}

// Run tries every waiting session once; a unit that still cannot be placed waits for the next run.
func (j *RetryJob) Run(ctx context.Context, _ jobs.Job) error {
	var afterID int64
	for {
		ids, err := j.sessions.SessionsWithUnplacedCommits(ctx, afterID, retryPageSize)
		if err != nil {
			return jobs.Retryablef("import.retry", string(RetryJobType), err)
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			placed, errs := j.committer.PlaceUnplaced(ctx, id)
			if placed > 0 || len(errs) > 0 {
				j.logger.Info("import retry", slog.Int64("session_id", id), slog.Int("placed", placed), slog.Int("not_placed", len(errs)))
			}
		}
		if len(ids) < retryPageSize {
			return nil
		}
		afterID = ids[len(ids)-1]
	}
}

// EnqueueRetry submits one retry sweep, deduplicated by RetryActionKey.
func EnqueueRetry(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: RetryJobType, ActionKey: RetryActionKey, Priority: jobs.PriorityNormal})
}
