-- Download folders (todo 4.18): watched for imports, with an optional library hint and their
-- own transfer order.

-- name: CreateDownloadFolder :one
INSERT INTO download_folder (path, label, category_id, readable, readable_reason, watch, transfer_ladder)
VALUES (sqlc.arg(path), sqlc.arg(label), sqlc.arg(category_id), sqlc.arg(readable), sqlc.arg(readable_reason),
        sqlc.arg(watch), sqlc.arg(transfer_ladder))
RETURNING id, path, label, category_id, readable, readable_reason, watch, transfer_ladder;

-- name: GetDownloadFolder :one
SELECT id, path, label, category_id, readable, readable_reason, watch, transfer_ladder
FROM download_folder WHERE id = ?;

-- name: ListDownloadFolders :many
SELECT id, path, label, category_id, readable, readable_reason, watch, transfer_ladder
FROM download_folder ORDER BY path;

-- name: UpdateDownloadFolder :exec
UPDATE download_folder SET label = ?, category_id = ?, watch = ?, transfer_ladder = ? WHERE id = ?;

-- name: DeleteDownloadFolder :exec
DELETE FROM download_folder WHERE id = ?;
