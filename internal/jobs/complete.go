package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/jobsdb"
)

// Outcome is how one attempt ended, recorded on job_attempt.
type Outcome string

// Attempt outcomes.
const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeRetrying  Outcome = "retrying"
	OutcomeReclaimed Outcome = "reclaimed"
)

// Complete marks a job succeeded and closes its attempt.
func (q *Queue) Complete(ctx context.Context, job Job, owner string, attempt Attempt) error {
	now := q.clock.Now()

	var affected int64
	err := q.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		queries := q.queries.WithTx(tx)
		rows, err := queries.CompleteJob(ctx, jobsdb.CompleteJobParams{
			UpdatedAt: now.Unix(), ID: job.ID, LeaseOwner: nullString(owner),
		})
		if err != nil {
			return err
		}
		affected = rows
		if rows == 0 {
			return nil
		}
		return queries.RecordAttemptFinish(ctx, jobsdb.RecordAttemptFinishParams{
			FinishedAt: sql.NullInt64{Int64: now.Unix(), Valid: true},
			Outcome:    nullString(string(OutcomeSucceeded)),
			Error:      sql.NullString{},
			ID:         attempt.ID,
		})
	})
	if err != nil {
		return fmt.Errorf("jobs: complete %d: %w", job.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: job %d", ErrLeaseLost, job.ID)
	}
	return nil
}

// Resolution is what Fail decided, so a caller can log it without re-deriving
// the classification.
type Resolution struct {
	Class Class
	// Retrying is false when the job has gone to FAILED.
	Retrying bool
	// RunAfter is when the retry is due; zero when not retrying.
	RunAfter time.Time
}

// Fail records a failed attempt and decides what happens next.
func (q *Queue) Fail(ctx context.Context, job Job, owner string, attempt Attempt, cause error) (Resolution, error) {
	class := Classify(cause)
	now := q.clock.Now()

	resolution := Resolution{Class: class}
	// job.Attempts is the count before this attempt; Start incremented the row.
	attemptsUsed := job.Attempts + 1
	if class == ClassRetryable && attemptsUsed < job.MaxAttempts {
		resolution.Retrying = true
		resolution.RunAfter = now.Add(q.jitter(backoffFor(attemptsUsed)))
	}

	message := ""
	if cause != nil {
		message = cause.Error()
	}

	var affected int64
	err := q.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		queries := q.queries.WithTx(tx)

		var rows int64
		var err error
		if resolution.Retrying {
			rows, err = queries.RetryJob(ctx, jobsdb.RetryJobParams{
				RunAfter:   resolution.RunAfter.Unix(),
				LastError:  nullString(message),
				UpdatedAt:  now.Unix(),
				ID:         job.ID,
				LeaseOwner: nullString(owner),
			})
		} else {
			rows, err = queries.FailJob(ctx, jobsdb.FailJobParams{
				LastError:  nullString(message),
				UpdatedAt:  now.Unix(),
				ID:         job.ID,
				LeaseOwner: nullString(owner),
			})
		}
		if err != nil {
			return err
		}
		affected = rows
		if rows == 0 {
			return nil
		}

		outcome := OutcomeFailed
		if resolution.Retrying {
			outcome = OutcomeRetrying
		}
		return queries.RecordAttemptFinish(ctx, jobsdb.RecordAttemptFinishParams{
			FinishedAt: sql.NullInt64{Int64: now.Unix(), Valid: true},
			Outcome:    nullString(string(outcome)),
			Error:      nullString(message),
			ID:         attempt.ID,
		})
	})
	if err != nil {
		return Resolution{}, fmt.Errorf("jobs: fail %d: %w", job.ID, err)
	}
	if affected == 0 {
		return Resolution{}, fmt.Errorf("%w: job %d", ErrLeaseLost, job.ID)
	}
	return resolution, nil
}

// Cancel stops a job that has not finished. A job already running keeps running
// until its worker notices; the state change is what the worker sees.
func (q *Queue) Cancel(ctx context.Context, id int64) error {
	return q.CancelWithReason(ctx, id, "cancelled")
}

// CancelWithReason cancels and records why, so a job that vanished from the
// queue can still explain itself.
func (q *Queue) CancelWithReason(ctx context.Context, id int64, reason string) error {
	now := q.clock.Now()

	var affected int64
	err := q.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := q.queries.WithTx(tx).CancelJob(ctx, jobsdb.CancelJobParams{
			Reason: nullString(reason),
			Now:    now.Unix(),
			ID:     id,
		})
		affected = rows
		return err
	})
	if err != nil {
		return fmt.Errorf("jobs: cancel %d: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: job %d is not cancellable", ErrNotFound, id)
	}
	return nil
}

// Reclaimed describes one job the reaper took back.
type Reclaimed struct {
	ID          int64
	Type        Type
	Pool        Pool
	Attempts    int
	MaxAttempts int
}

// ReclaimExpired returns abandoned jobs to the queue.
func (q *Queue) ReclaimExpired(ctx context.Context) ([]Reclaimed, error) {
	now := q.clock.Now()

	var rows []jobsdb.ReclaimExpiredLeasesRow
	err := q.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		reclaimed, err := q.queries.WithTx(tx).ReclaimExpiredLeases(ctx, jobsdb.ReclaimExpiredLeasesParams{
			Reason: nullString("lease expired; the worker holding it stopped without reporting"),
			Now:    now.Unix(),
		})
		rows = reclaimed
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: reclaim expired leases: %w", err)
	}

	reclaimed := make([]Reclaimed, 0, len(rows))
	for _, row := range rows {
		reclaimed = append(reclaimed, Reclaimed{
			ID:          row.ID,
			Type:        Type(row.Type),
			Pool:        Pool(row.Pool),
			Attempts:    int(row.Attempts),
			MaxAttempts: int(row.MaxAttempts),
		})
	}
	return reclaimed, nil
}

// Attempts lists a job's recorded attempts, oldest first.
func (q *Queue) Attempts(ctx context.Context, jobID int64) ([]Attempt, error) {
	rows, err := q.queries.ListAttempts(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("jobs: list attempts for %d: %w", jobID, err)
	}
	attempts := make([]Attempt, 0, len(rows))
	for _, row := range rows {
		attempts = append(attempts, convertAttempt(row))
	}
	return attempts, nil
}

// Counts reports how many jobs sit in each state, per pool, for the dashboard
// and the mnx_job_queue_depth metric.
func (q *Queue) Counts(ctx context.Context) (map[State]map[Pool]int64, error) {
	rows, err := q.queries.CountJobsByState(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs: count by state: %w", err)
	}
	counts := make(map[State]map[Pool]int64)
	for _, row := range rows {
		state := State(row.State)
		if counts[state] == nil {
			counts[state] = make(map[Pool]int64)
		}
		counts[state][Pool(row.Pool)] = row.Total
	}
	return counts, nil
}

// fullJitter spreads a backoff over [0, d].
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d)) + 1)
}

// NoJitter returns a backoff unchanged, for tests and deterministic mode.
func NoJitter(d time.Duration) time.Duration { return d }
