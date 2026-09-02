-- Autonomous discovery jobs (ADR-38). Policy, so this stays in settings.db.

-- name: CreateDiscoveryJob :one
INSERT INTO discovery_job (name, source_json, media_type, category_id, filters_json,
                          scoring_json, limit_per_run, action, schedule, enabled)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, source_json, media_type, category_id, filters_json, scoring_json,
          limit_per_run, action, schedule, enabled, last_run_at;

-- name: GetDiscoveryJob :one
SELECT id, name, source_json, media_type, category_id, filters_json, scoring_json,
       limit_per_run, action, schedule, enabled, last_run_at
FROM discovery_job WHERE id = ?;

-- name: ListDiscoveryJobs :many
SELECT id, name, source_json, media_type, category_id, filters_json, scoring_json,
       limit_per_run, action, schedule, enabled, last_run_at
FROM discovery_job ORDER BY name;

-- What a scheduler actually runs -- off until previewed (ADR-38).
-- name: ListEnabledDiscoveryJobs :many
SELECT id, name, source_json, media_type, category_id, filters_json, scoring_json,
       limit_per_run, action, schedule, enabled, last_run_at
FROM discovery_job WHERE enabled = 1 ORDER BY name;

-- name: UpdateDiscoveryJob :exec
UPDATE discovery_job
SET source_json = ?, media_type = ?, category_id = ?, filters_json = ?,
    scoring_json = ?, limit_per_run = ?, action = ?, schedule = ?
WHERE id = ?;

-- The enable transition on its own: this is the "preview before enabling" gate, a
-- deliberate act distinct from editing a job's policy.
-- name: SetDiscoveryJobEnabled :exec
UPDATE discovery_job SET enabled = ? WHERE id = ?;

-- name: RecordDiscoveryJobRun :exec
UPDATE discovery_job SET last_run_at = ? WHERE id = ?;

-- name: DeleteDiscoveryJob :exec
DELETE FROM discovery_job WHERE id = ?;

-- Invariant 41: deleting a category in use is refused with a count of what
-- references it.
-- name: CountDiscoveryJobsUsingCategory :one
SELECT count(*) FROM discovery_job WHERE category_id = ?;
