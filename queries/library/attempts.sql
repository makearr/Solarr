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
