-- Download folders (migration 0010, todo 4.18, section 5.1). Column lists
-- follow table order, or sqlc stops reusing models.

-- name: CreateDownloadFolder :one
INSERT INTO download_folder (path, label, category_id, readable, readable_reason)
VALUES (sqlc.arg(path), sqlc.arg(label), sqlc.arg(category_id), sqlc.arg(readable), sqlc.arg(readable_reason))
RETURNING id, path, label, category_id, readable, readable_reason;

-- name: GetDownloadFolder :one
SELECT id, path, label, category_id, readable, readable_reason
FROM download_folder WHERE id = ?;

-- name: ListDownloadFolders :many
SELECT id, path, label, category_id, readable, readable_reason
FROM download_folder ORDER BY path;

-- name: DeleteDownloadFolder :exec
DELETE FROM download_folder WHERE id = ?;
