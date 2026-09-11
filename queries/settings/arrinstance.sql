-- *Arr instance connections (todo 4.13, ADR-52). Column lists follow table order.

-- name: CreateArrInstance :one
INSERT INTO arr_instance (app, name, base_url, api_key, category_id, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at,
          last_synced_at, last_sync_error;

-- name: GetArrInstance :one
SELECT id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at,
       last_synced_at, last_sync_error
FROM arr_instance WHERE id = ?;

-- name: ListArrInstances :many
SELECT id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at,
       last_synced_at, last_sync_error
FROM arr_instance ORDER BY app, name;

-- name: UpdateArrInstance :exec
UPDATE arr_instance
SET name = ?, base_url = ?, category_id = ?, enabled = ?, updated_at = ?
WHERE id = ?;

-- A separate statement from UpdateArrInstance so a caller that is not
-- changing the key never re-seals and rewrites it -- Seal generates a fresh
-- nonce every call, so writing it back unconditionally would be silent,
-- pointless churn on every unrelated edit.
-- name: SetArrInstanceAPIKey :exec
UPDATE arr_instance SET api_key = ?, updated_at = ? WHERE id = ?;

-- name: SetArrInstanceSyncResult :exec
UPDATE arr_instance SET last_synced_at = ?, last_sync_error = ? WHERE id = ?;

-- name: DeleteArrInstance :exec
DELETE FROM arr_instance WHERE id = ?;
