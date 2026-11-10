-- Adds to what an hour already holds.
-- name: AddTaskHour :exec
INSERT INTO task_hour (job_type, hour_start, run_ms, runs) VALUES (?, ?, ?, ?)
ON CONFLICT (job_type, hour_start) DO UPDATE SET run_ms = run_ms + excluded.run_ms, runs = runs + excluded.runs;

-- name: ListTaskHoursSince :many
SELECT job_type, hour_start, run_ms, runs FROM task_hour WHERE hour_start >= sqlc.arg(since) ORDER BY hour_start, job_type;

-- name: DeleteTaskHoursBefore :execrows
DELETE FROM task_hour WHERE hour_start < sqlc.arg(before);
