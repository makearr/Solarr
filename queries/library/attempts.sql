-- Identification attempts (migration 0033). Column lists follow table order.

-- name: InsertIdentificationAttempt :exec
INSERT INTO identification_attempt (media_object_id, attempted_at, outcome, passes_json)
VALUES (sqlc.arg(media_object_id), sqlc.arg(attempted_at), sqlc.arg(outcome), sqlc.arg(passes_json));

-- Keeps only the newest keep attempts for one file.
-- name: PruneIdentificationAttempts :exec
DELETE FROM identification_attempt
WHERE identification_attempt.media_object_id = sqlc.arg(media_object_id)
  AND identification_attempt.id NOT IN (
    SELECT newest.id FROM identification_attempt AS newest
    WHERE newest.media_object_id = sqlc.arg(media_object_id)
    ORDER BY newest.attempted_at DESC, newest.id DESC
    LIMIT sqlc.arg(keep)
  );

-- name: ListIdentificationAttempts :many
SELECT id, media_object_id, attempted_at, outcome, passes_json
FROM identification_attempt
WHERE media_object_id = ?
ORDER BY attempted_at DESC, id DESC;

-- One page of queued files with what their newest attempt concluded. The caller
-- filters on the pass and outcome it cares about (D12's "re-run what a missing
-- AcoustID key skipped"): sqlc's SQLite parser cannot model json_each's columns
-- in a WHERE, and decoding one bounded page in Go is the same discipline every
-- cross-database composition here already uses.
-- name: ListQueuedWithNewestAttempt :many
SELECT queue.media_object_id, queue.root_folder_id, queue.rel_path, attempt.passes_json
FROM identification_queue AS queue
JOIN identification_attempt AS attempt ON attempt.media_object_id = queue.media_object_id
WHERE attempt.id = (
    SELECT newest.id FROM identification_attempt AS newest
    WHERE newest.media_object_id = queue.media_object_id
    ORDER BY newest.attempted_at DESC, newest.id DESC
    LIMIT 1
  )
  AND queue.media_object_id > sqlc.arg(after_id)
ORDER BY queue.media_object_id
LIMIT sqlc.arg(page_size);
