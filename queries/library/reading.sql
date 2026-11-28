-- name: GetReadingPosition :one
SELECT user_id, media_object_id, position, progress, updated_at
FROM reading_position WHERE user_id = sqlc.arg(user_id) AND media_object_id = sqlc.arg(media_object_id);

-- name: SetReadingPosition :exec
INSERT INTO reading_position (user_id, media_object_id, position, progress, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(media_object_id), sqlc.arg(position), sqlc.arg(progress), sqlc.arg(updated_at))
ON CONFLICT (user_id, media_object_id) DO UPDATE SET position = excluded.position, progress = excluded.progress, updated_at = excluded.updated_at;
