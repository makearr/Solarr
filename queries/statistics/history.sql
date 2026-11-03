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

-- name: MoveHistoryToWork :exec
UPDATE history SET work_id = sqlc.arg(to_work_id) WHERE work_id = sqlc.arg(from_work_id);

-- Grabs, imports and failures per library and indexer since a time; history keeps the indexer's name as it was then.
-- name: AggregateHistoryByIndexer :many
SELECT category_id, indexer, CAST(REPLACE(kind, 'download.', '') AS TEXT) AS kind, CAST(COUNT(*) AS INTEGER) AS events
FROM history
WHERE at >= sqlc.arg(since) AND indexer <> '' AND kind IN ('download.grabbed', 'download.imported', 'download.failed')
GROUP BY category_id, indexer, kind
ORDER BY category_id, indexer, kind;
