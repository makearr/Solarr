-- name: GetSimilarReport :one
SELECT level, computed_at, groups_json FROM similar_report WHERE level = ?;

-- name: UpsertSimilarReport :exec
INSERT INTO similar_report (level, computed_at, groups_json) VALUES (?, ?, ?)
ON CONFLICT(level) DO UPDATE SET computed_at = excluded.computed_at, groups_json = excluded.groups_json;

-- Which of the given media object ids still have a live primary path. ids is a JSON array.
-- name: ListVideoIDsWithLivePath :many
SELECT DISTINCT lp.media_object_id FROM library_path lp
WHERE lp.media_object_id IN (SELECT value FROM json_each(sqlc.arg(ids))) AND lp.is_primary = 1 AND lp.missing_since IS NULL
ORDER BY lp.media_object_id;
