-- name: ListCategories :many
SELECT id, media_type, name, priority_rank, quality_profile_id, language_profile_id
FROM category ORDER BY priority_rank, name;

-- name: GetCategory :one
SELECT id, media_type, name, priority_rank, quality_profile_id, language_profile_id
FROM category WHERE id = ?;

-- name: CreateCategory :one
INSERT INTO category (media_type, name, priority_rank)
VALUES (?, ?, ?)
RETURNING id, media_type, name, priority_rank, quality_profile_id, language_profile_id;

-- name: UpdateCategory :exec
UPDATE category SET name = ?, priority_rank = ? WHERE id = ?;

-- name: DeleteCategory :exec
DELETE FROM category WHERE id = ?;

-- name: ListRootFolders :many
SELECT id, path, label, online, writable, writable_reason, read_only, allow_rename,
       total_bytes, free_bytes, min_free_bytes, capacity_checked_at, fs_id, capabilities_json
FROM root_folder ORDER BY path;

-- name: GetRootFolder :one
SELECT id, path, label, online, writable, writable_reason, read_only, allow_rename,
       total_bytes, free_bytes, min_free_bytes, capacity_checked_at, fs_id, capabilities_json
FROM root_folder WHERE id = ?;

-- name: CreateRootFolder :one
INSERT INTO root_folder (path, label, online)
VALUES (?, ?, ?)
RETURNING id, path, label, online, writable, writable_reason, read_only, allow_rename,
          total_bytes, free_bytes, min_free_bytes, capacity_checked_at, fs_id, capabilities_json;

-- name: SetRootFolderOnline :exec
UPDATE root_folder SET online = ? WHERE id = ?;

-- name: SetRootFolderPolicy :exec
-- read_only and allow_rename are the user's own policy (section 11.7,
-- Invariant 69); min_free_bytes is their reserve. Kept separate from
-- SetRootFolderCapacity below, which only the capacity job ever calls.
UPDATE root_folder SET read_only = ?, allow_rename = ?, min_free_bytes = ? WHERE id = ?;

-- name: SetRootFolderCapacity :exec
-- The capacity-polling job's own write: everything it probes, in one
-- statement, so a root's reading is never partially updated.
UPDATE root_folder
SET writable = ?, writable_reason = ?, total_bytes = ?, free_bytes = ?,
    capacity_checked_at = ?, fs_id = ?, capabilities_json = ?
WHERE id = ?;

-- name: DeleteRootFolder :exec
DELETE FROM root_folder WHERE id = ?;

-- name: GetPlacementAffinity :one
SELECT root_folder_id FROM placement_affinity WHERE category_id = ? AND affinity_key = ?;

-- name: SetPlacementAffinity :exec
INSERT INTO placement_affinity (category_id, affinity_key, root_folder_id, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (category_id, affinity_key) DO UPDATE
  SET root_folder_id = excluded.root_folder_id, updated_at = excluded.updated_at;

-- name: ListCategoryRootFolders :many
SELECT category_id, root_folder_id, ord, weight, accepts_new
FROM category_root_folder ORDER BY category_id, ord;

-- name: AttachRootFolderToCategory :exec
INSERT INTO category_root_folder (category_id, root_folder_id, ord, weight, accepts_new)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (category_id, root_folder_id) DO UPDATE
  SET ord = excluded.ord, weight = excluded.weight, accepts_new = excluded.accepts_new;

-- name: DetachRootFolderFromCategory :exec
DELETE FROM category_root_folder WHERE category_id = ? AND root_folder_id = ?;

-- Invariant 41: deleting an entity in use is refused with a count of what
-- references it, so the user gets a reason rather than a constraint violation.

-- name: CountCategoriesUsingRootFolder :one
SELECT count(*) FROM category_root_folder WHERE root_folder_id = ?;

-- name: CountRootFoldersInCategory :one
SELECT count(*) FROM category_root_folder WHERE category_id = ?;

-- name: CountUsersWithCategoryAccess :one
SELECT count(*) FROM user_category_access WHERE category_id = ?;
