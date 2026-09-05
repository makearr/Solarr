-- ADR-51 (migration 0020). Column lists follow table order, or sqlc stops
-- reusing models.

-- name: CreateMoveJob :one
INSERT INTO move_job (reason, source_root_id, state, files_total, bytes_total, plan_json, created_at)
VALUES (sqlc.arg(reason), sqlc.arg(source_root_id), 'PLANNED', sqlc.arg(files_total),
        sqlc.arg(bytes_total), sqlc.arg(plan_json), sqlc.arg(created_at))
RETURNING id, reason, source_root_id, state, files_total, files_done, bytes_total, bytes_done,
          plan_json, created_at, finished_at, error;

-- name: GetMoveJob :one
SELECT id, reason, source_root_id, state, files_total, files_done, bytes_total, bytes_done,
       plan_json, created_at, finished_at, error
FROM move_job WHERE id = ?;

-- Small, operator-facing list -- move counts never approach library scale,
-- the same reasoning ListImportSessions' own comment already gives.
-- name: ListMoveJobs :many
SELECT id, reason, source_root_id, state, files_total, files_done, bytes_total, bytes_done,
       plan_json, created_at, finished_at, error
FROM move_job
WHERE sqlc.arg(filter_by_state) = 0 OR state = sqlc.arg(state)
ORDER BY created_at DESC;

-- name: SetMoveJobRunning :exec
UPDATE move_job SET state = 'RUNNING' WHERE id = ?;

-- name: FinishMoveJob :exec
UPDATE move_job SET state = sqlc.arg(state), finished_at = sqlc.arg(finished_at), error = sqlc.arg(error)
WHERE id = sqlc.arg(id);

-- One row per call, looped inside one transaction by the caller (the same
-- shape ImportSessions.AddUnits already uses) -- modernc's sqlite driver
-- does not support :copyfrom (pgx/mysql only).
-- name: InsertMoveItem :exec
INSERT INTO move_item (move_job_id, media_object_id, affinity_key, dest_root_id, dest_rel_path, bytes, state)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- Oldest (insertion order, via rowid on a WITHOUT ROWID table's own implicit
-- ordering by the primary key) pending item first -- Apply works through a
-- job's own items one at a time, resumable: a restart just asks again.
-- name: NextPendingMoveItem :one
SELECT move_job_id, media_object_id, affinity_key, dest_root_id, dest_rel_path, bytes, state, error
FROM move_item WHERE move_job_id = ? AND state != 'DONE' AND state != 'FAILED'
ORDER BY media_object_id LIMIT 1;

-- name: SetMoveItemState :exec
UPDATE move_item SET state = sqlc.arg(state), error = sqlc.arg(error)
WHERE move_job_id = sqlc.arg(move_job_id) AND media_object_id = sqlc.arg(media_object_id);

-- Recomputed from move_item rather than incremented as items complete -- the
-- same drift-and-repair shape RecomputeImportSessionCounts already uses.
-- name: RecomputeMoveJobProgress :exec
UPDATE move_job SET
  files_done = (SELECT COUNT(*) FROM move_item mi WHERE mi.move_job_id = sqlc.arg(move_job_id) AND mi.state = 'DONE'),
  bytes_done = (SELECT COALESCE(SUM(mi.bytes), 0) FROM move_item mi
                WHERE mi.move_job_id = sqlc.arg(move_job_id) AND mi.state = 'DONE')
WHERE id = sqlc.arg(move_job_id);

-- name: ListMoveItems :many
SELECT move_job_id, media_object_id, affinity_key, dest_root_id, dest_rel_path, bytes, state, error
FROM move_item WHERE move_job_id = ? ORDER BY media_object_id;
