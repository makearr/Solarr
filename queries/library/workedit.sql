-- name: GetDesiredProfileOverride :one
SELECT profile_id FROM desired_profile_override WHERE desired_item_id = ?;

-- name: SetDesiredProfileOverride :exec
INSERT INTO desired_profile_override (desired_item_id, profile_id) VALUES (?, ?)
ON CONFLICT (desired_item_id) DO UPDATE SET profile_id = excluded.profile_id;

-- name: ClearDesiredProfileOverride :exec
DELETE FROM desired_profile_override WHERE desired_item_id = ?;

-- name: ListTagNames :many
SELECT id, name FROM tag ORDER BY name COLLATE NOCASE, id;

-- name: ListTagNamesForSubjectBySource :many
SELECT t.id AS tag_id, t.name AS tag_name
FROM tag_assignment a
JOIN tag t ON t.id = a.tag_id
WHERE a.subject_type = ? AND a.subject_id = ? AND a.source = ?
ORDER BY t.name COLLATE NOCASE, t.id;

-- A tag is the user's own when it was created from the Edit dialog; a provider's genre is not offered for renaming.
-- name: ListUserTagsWithUse :many
SELECT t.id, t.name, CAST(COUNT(a.subject_id) AS INTEGER) AS works
FROM tag t
LEFT JOIN tag_assignment a ON a.tag_id = t.id AND a.subject_type = 'work'
WHERE t.source = 'user'
GROUP BY t.id, t.name
ORDER BY t.name COLLATE NOCASE, t.id;

-- name: RenameUserTag :execrows
UPDATE tag SET name = ?, slug = ? WHERE id = ? AND source = 'user';

-- The assignments, closure rows and aliases go with it (ON DELETE CASCADE).
-- name: DeleteUserTag :execrows
DELETE FROM tag WHERE id = ? AND source = 'user';
