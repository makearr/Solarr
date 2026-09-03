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
SELECT id, path, label, online FROM root_folder ORDER BY path;

-- name: GetRootFolder :one
SELECT id, path, label, online FROM root_folder WHERE id = ?;

-- name: CreateRootFolder :one
INSERT INTO root_folder (path, label, online)
VALUES (?, ?, ?)
RETURNING id, path, label, online;

-- name: SetRootFolderOnline :exec
UPDATE root_folder SET online = ? WHERE id = ?;

-- name: DeleteRootFolder :exec
DELETE FROM root_folder WHERE id = ?;

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
