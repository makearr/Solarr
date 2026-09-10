-- ADR-59's task registry own run-state (migration 0004). One row per task
-- key, upserted after every fire.

-- name: UpsertScheduleState :exec
INSERT INTO schedule_state (task_key, last_run_at, last_duration_ms, last_outcome, last_error, next_run_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (task_key) DO UPDATE SET
  last_run_at = excluded.last_run_at,
  last_duration_ms = excluded.last_duration_ms,
  last_outcome = excluded.last_outcome,
  last_error = excluded.last_error,
  next_run_at = excluded.next_run_at;

-- Called every tick, whether or not a fire actually happens (enabled=false
-- still has a next-fire time to show), so it must never disturb last_run_at/
-- last_duration_ms/last_outcome/last_error -- only UpsertScheduleState (after
-- a real fire) writes those, or a restart's own first tick would wipe out
-- the only evidence a task has not run in a week.
-- name: UpsertScheduleNextRun :exec
INSERT INTO schedule_state (task_key, next_run_at)
VALUES (?, ?)
ON CONFLICT (task_key) DO UPDATE SET next_run_at = excluded.next_run_at;

-- name: GetScheduleState :one
SELECT task_key, last_run_at, last_duration_ms, last_outcome, last_error, next_run_at
FROM schedule_state WHERE task_key = ?;

-- name: ListScheduleStates :many
SELECT task_key, last_run_at, last_duration_ms, last_outcome, last_error, next_run_at
FROM schedule_state;
