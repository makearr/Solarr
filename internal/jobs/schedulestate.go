package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/jobsdb"
)

// ScheduleRun is one task's own persisted run-state (migration 0004) --
// what a restart must not erase (ADR-59: "the only evidence something has
// not run in a week").
type ScheduleRun struct {
	TaskKey        string
	LastRunAt      *time.Time
	LastDurationMs *int64
	LastOutcome    string
	LastError      string
	NextRunAt      *time.Time
}

// ScheduleStates persists ADR-59's own run-state over jobs.db. One row per
// task key.
type ScheduleStates struct {
	store   *store.Store
	queries *jobsdb.Queries
}

// NewScheduleStates builds a reader/writer over jobStore.
func NewScheduleStates(jobStore *store.Store) *ScheduleStates {
	return &ScheduleStates{store: jobStore, queries: jobsdb.New(jobStore.Read())}
}

// SetNextRun records when a task will next fire, without disturbing
// last_run_at/last_duration_ms/last_outcome/last_error -- called every tick,
// whether or not the task actually fires, so a disabled task still shows an
// accurate next-run time.
func (s *ScheduleStates) SetNextRun(ctx context.Context, taskKey string, next time.Time) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return jobsdb.New(tx).UpsertScheduleNextRun(ctx, jobsdb.UpsertScheduleNextRunParams{
			TaskKey: taskKey, NextRunAt: sql.NullInt64{Int64: next.Unix(), Valid: !next.IsZero()},
		})
	})
	if err != nil {
		return fmt.Errorf("jobs: set next run for %s: %w", taskKey, err)
	}
	return nil
}

// RecordRun writes what one fire actually did.
func (s *ScheduleStates) RecordRun(
	ctx context.Context, taskKey string, ran time.Time, duration time.Duration,
	outcome, errMsg string, next time.Time,
) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return jobsdb.New(tx).UpsertScheduleState(ctx, jobsdb.UpsertScheduleStateParams{
			TaskKey:        taskKey,
			LastRunAt:      sql.NullInt64{Int64: ran.Unix(), Valid: true},
			LastDurationMs: sql.NullInt64{Int64: duration.Milliseconds(), Valid: true},
			LastOutcome:    sql.NullString{String: outcome, Valid: outcome != ""},
			LastError:      sql.NullString{String: errMsg, Valid: errMsg != ""},
			NextRunAt:      sql.NullInt64{Int64: next.Unix(), Valid: !next.IsZero()},
		})
	})
	if err != nil {
		return fmt.Errorf("jobs: record run for %s: %w", taskKey, err)
	}
	return nil
}

// Get answers one task's own state. found is false for a task that has
// never ticked yet.
func (s *ScheduleStates) Get(ctx context.Context, taskKey string) (ScheduleRun, bool, error) {
	row, err := s.queries.GetScheduleState(ctx, taskKey)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleRun{}, false, nil
	}
	if err != nil {
		return ScheduleRun{}, false, fmt.Errorf("jobs: get schedule state for %s: %w", taskKey, err)
	}
	return convertScheduleRun(row), true, nil
}

// List answers every task that has ticked at least once.
func (s *ScheduleStates) List(ctx context.Context) ([]ScheduleRun, error) {
	rows, err := s.queries.ListScheduleStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs: list schedule states: %w", err)
	}
	states := make([]ScheduleRun, len(rows))
	for index, row := range rows {
		states[index] = convertScheduleRun(row)
	}
	return states, nil
}

func convertScheduleRun(row jobsdb.ScheduleState) ScheduleRun {
	run := ScheduleRun{TaskKey: row.TaskKey}
	if row.LastRunAt.Valid {
		t := time.Unix(row.LastRunAt.Int64, 0).UTC()
		run.LastRunAt = &t
	}
	if row.LastDurationMs.Valid {
		run.LastDurationMs = &row.LastDurationMs.Int64
	}
	run.LastOutcome = row.LastOutcome.String
	run.LastError = row.LastError.String
	if row.NextRunAt.Valid {
		t := time.Unix(row.NextRunAt.Int64, 0).UTC()
		run.NextRunAt = &t
	}
	return run
}
