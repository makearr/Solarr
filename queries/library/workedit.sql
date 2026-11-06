-- name: GetDesiredProfileOverride :one
SELECT profile_id FROM desired_profile_override WHERE desired_item_id = ?;

-- name: SetDesiredProfileOverride :exec
INSERT INTO desired_profile_override (desired_item_id, profile_id) VALUES (?, ?)
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
