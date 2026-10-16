-- A work's release filters (stage 8 T2c): required, denied and preferred words, allowed and denied release groups.

-- name: ListWorkReleaseFilters :many
SELECT id, work_id, media_ref_prefix, kind, value, is_regex, score, created_at
FROM work_release_filter WHERE work_id = ? ORDER BY id;

-- name: CreateWorkReleaseFilter :exec
INSERT INTO work_release_filter (work_id, media_ref_prefix, kind, value, is_regex, score, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (work_id, media_ref_prefix, kind, value) DO UPDATE SET is_regex = excluded.is_regex, score = excluded.score;

-- name: DeleteWorkReleaseFilters :exec
DELETE FROM work_release_filter WHERE work_id = ?;
