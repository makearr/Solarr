-- name: GetDesiredProfileOverride :one
SELECT profile_id FROM desired_profile_override WHERE desired_item_id = ?;

-- name: SetDesiredProfileOverride :exec
INSERT INTO desired_profile_override (desired_item_id, profile_id) VALUES (?, ?)
ON CONFLICT (desired_item_id) DO UPDATE SET profile_id = excluded.profile_id;

-- name: GetDesiredOverrides :one
SELECT CAST(COALESCE((SELECT p.profile_id FROM desired_profile_override p WHERE p.desired_item_id = sqlc.arg(item_id)), 0) AS INTEGER) AS profile_id,
       CAST(COALESCE((SELECT t.target_id FROM desired_target_override t WHERE t.desired_item_id = sqlc.arg(item_id)), 0) AS INTEGER) AS target_id;

-- name: SetDesiredProfileOverrideForWork :exec
INSERT INTO desired_profile_override (desired_item_id, profile_id)
SELECT id, sqlc.arg(profile_id) FROM desired_item WHERE work_id = sqlc.arg(work_id)
ON CONFLICT (desired_item_id) DO UPDATE SET profile_id = excluded.profile_id;

-- name: ClearDesiredProfileOverride :exec
DELETE FROM desired_profile_override WHERE desired_item_id = ?;

-- name: ListTagNamesForSubjectBySource :many
SELECT t.id AS tag_id, t.name AS tag_name
FROM tag_assignment a
JOIN tag t ON t.id = a.tag_id
WHERE a.subject_type = ? AND a.subject_id = ? AND a.source = ?
ORDER BY t.name COLLATE NOCASE, t.id;

-- name: GetDesiredTargetOverride :one
SELECT target_id FROM desired_target_override WHERE desired_item_id = ?;

-- name: SetDesiredTargetOverride :exec
INSERT INTO desired_target_override (desired_item_id, target_id) VALUES (?, ?)
ON CONFLICT (desired_item_id) DO UPDATE SET target_id = excluded.target_id;

-- name: ClearDesiredTargetOverride :exec
DELETE FROM desired_target_override WHERE desired_item_id = ?;

-- name: PinOffDefaultProfiles :execrows
INSERT INTO desired_profile_override (desired_item_id, profile_id)
SELECT d.id, d.profile_id FROM desired_item d
WHERE d.category_id = sqlc.arg(category_id) AND d.profile_id <> sqlc.arg(default_profile_id)
  AND NOT EXISTS (SELECT 1 FROM desired_profile_override o WHERE o.desired_item_id = d.id)
ORDER BY d.id LIMIT sqlc.arg(page_size);
