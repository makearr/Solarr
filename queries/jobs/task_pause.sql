-- name: PauseTask :exec
INSERT INTO task_pause (scope, paused_at) VALUES (?, ?)
ON CONFLICT (scope) DO NOTHING;

-- name: ResumeTask :exec
DELETE FROM task_pause WHERE scope = ?;

-- name: ListTaskPauses :many
SELECT scope, paused_at FROM task_pause;

-- name: RecordJobTask :exec
INSERT INTO job_task (job_id, task_key) VALUES (?, ?)
ON CONFLICT (job_id) DO NOTHING;

-- name: GetJobTask :one
SELECT task_key FROM job_task WHERE job_id = ?;

-- Jobs a pause is holding or has interrupted: waiting in the queue, not yet finished.
-- name: CountHeldJobsByTask :many
SELECT job_task.task_key AS task_key, CAST(count(*) AS INTEGER) AS held
FROM job_task
JOIN job ON job.id = job_task.job_id
WHERE job.state IN ('PENDING', 'PENDING_RETRY')
GROUP BY job_task.task_key;
