-- ADR-53 part 1 (migration 0007). Column lists follow table order, or sqlc
-- stops reusing models.

-- name: CreateExclusionPattern :one
INSERT INTO exclusion_pattern (pattern, action, media_type, category_id, note, builtin, enabled)
VALUES (sqlc.arg(pattern), sqlc.arg(action), sqlc.arg(media_type), sqlc.arg(category_id),
        sqlc.arg(note), sqlc.arg(builtin), sqlc.arg(enabled))
RETURNING id, pattern, action, media_type, category_id, note, builtin, enabled;

-- name: GetExclusionPattern :one
SELECT id, pattern, action, media_type, category_id, note, builtin, enabled
FROM exclusion_pattern WHERE id = ?;

-- name: ListExclusionPatterns :many
SELECT id, pattern, action, media_type, category_id, note, builtin, enabled
FROM exclusion_pattern ORDER BY id;

-- name: UpdateExclusionPattern :exec
UPDATE exclusion_pattern SET pattern = ?, action = ?, media_type = ?, category_id = ?,
                              note = ?, enabled = ?
WHERE id = ?;

-- name: DeleteExclusionPattern :exec
DELETE FROM exclusion_pattern WHERE id = ?;
