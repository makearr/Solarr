-- Paths a media server has still to be told about. Column lists follow table order.

-- name: AddMediaServerPending :exec
INSERT OR IGNORE INTO media_server_pending (server_id, category_id, path, change, work_id, queued_at, attempts)
VALUES (?, ?, ?, ?, ?, ?, 0);

-- name: ListDueMediaServerPending :many
SELECT id, server_id, category_id, path, change, work_id, queued_at, attempts FROM media_server_pending
WHERE server_id = ? AND queued_at <= ? ORDER BY queued_at, id LIMIT ?;

-- name: ListMediaServerPendingServers :many
SELECT DISTINCT server_id FROM media_server_pending WHERE queued_at <= ? ORDER BY server_id;

-- name: DeleteMediaServerPending :exec
DELETE FROM media_server_pending WHERE id IN (SELECT value FROM json_each(sqlc.arg(ids)));

-- name: BumpMediaServerPending :exec
UPDATE media_server_pending SET attempts = attempts + 1 WHERE id IN (SELECT value FROM json_each(sqlc.arg(ids)));

-- name: DeleteMediaServerPendingOf :exec
DELETE FROM media_server_pending WHERE server_id = ?;

-- name: ListWorkFilesForPush :many
SELECT DISTINCT lp.root_folder_id, lp.rel_path, lp.created_at
FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id
WHERE wf.work_id = ? AND lp.missing_since IS NULL
ORDER BY lp.root_folder_id, lp.rel_path;

-- name: ListWorkTitlesForPush :many
SELECT id, canonical_title FROM work WHERE id IN (SELECT value FROM json_each(sqlc.arg(ids))) ORDER BY id;
