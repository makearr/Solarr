-- name: AddWorkExclusion :exec
INSERT INTO work_exclusion (media_type, provider, external_id, title, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET title = excluded.title, media_type = excluded.media_type;

-- name: DeleteWorkExclusion :exec
DELETE FROM work_exclusion WHERE provider = ? AND external_id = ?;

-- name: ListWorkExclusions :many
SELECT media_type, provider, external_id, title, created_at FROM work_exclusion
WHERE media_type = ? ORDER BY title COLLATE NOCASE, provider, external_id;

-- name: CountWorkExclusion :one
SELECT CAST(COUNT(*) AS INTEGER) AS excluded FROM work_exclusion WHERE provider = ? AND external_id = ?;
