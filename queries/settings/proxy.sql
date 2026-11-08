-- Proxies an indexer can use. Column lists follow table order.

-- name: CreateProxy :one
INSERT INTO proxy (name, kind, url, source_arr_id, source_external_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, kind, url, source_arr_id, source_external_id, created_at, updated_at;

-- name: GetProxy :one
SELECT id, name, kind, url, source_arr_id, source_external_id, created_at, updated_at
FROM proxy WHERE id = ?;

-- name: ListProxies :many
SELECT id, name, kind, url, source_arr_id, source_external_id, created_at, updated_at
FROM proxy ORDER BY name;

-- name: UpdateProxy :exec
UPDATE proxy SET name = ?, kind = ?, url = ?, updated_at = ? WHERE id = ?;

-- name: DeleteProxy :exec
DELETE FROM proxy WHERE id = ?;
