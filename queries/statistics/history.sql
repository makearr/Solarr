-- name: AppendHistory :one
INSERT INTO history (at, kind, category_id, work_id, acquisition_id, release_title, work_title, lead_name, indexer, client, detail, event_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- An event already copied is left as it is.
-- name: CopyHistoryEvent :exec
INSERT OR IGNORE INTO history (at, kind, category_id, work_id, acquisition_id, release_title, work_title, lead_name, indexer, client, detail, event_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: LastCopiedHistoryEvent :one
SELECT CAST(COALESCE(MAX(event_id), 0) AS INTEGER) AS event_id FROM history;

-- One page of history, newest first, of one work or of every one with work_id 0.
-- name: ListHistory :many
SELECT id, at, kind, category_id, work_id, acquisition_id, release_title, work_title, lead_name, indexer, client, detail, event_id
FROM history
WHERE (CAST(sqlc.arg(work_id) AS INTEGER) = 0 OR work_id = sqlc.arg(work_id))
  AND (CAST(sqlc.arg(before_id) AS INTEGER) = 0 OR id < sqlc.arg(before_id))
ORDER BY id DESC
LIMIT sqlc.arg(page_size);
