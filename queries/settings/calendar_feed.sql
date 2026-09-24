-- name: UpsertCalendarFeed :exec
INSERT INTO calendar_feed (user_id, token_hash, token_sealed, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET
  token_hash = excluded.token_hash, token_sealed = excluded.token_sealed, created_at = excluded.created_at;

-- name: GetCalendarFeed :one
SELECT user_id, token_hash, token_sealed, created_at FROM calendar_feed WHERE user_id = ?;

-- name: GetCalendarFeedByHash :one
SELECT user_id, token_hash, token_sealed, created_at FROM calendar_feed WHERE token_hash = ?;

-- name: DeleteCalendarFeed :exec
DELETE FROM calendar_feed WHERE user_id = ?;
