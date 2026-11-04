-- Hearts, per user; a favorite is not a follow. Column lists follow table order.

-- name: AddFavorite :exec
INSERT INTO favorite (user_id, subject_type, subject_id, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, subject_type, subject_id) DO NOTHING;

-- name: RemoveFavorite :exec
DELETE FROM favorite WHERE user_id = ? AND subject_type = ? AND subject_id = ?;

-- name: ListFavoriteIDs :many
SELECT subject_id FROM favorite WHERE user_id = ? AND subject_type = ? ORDER BY subject_id;
