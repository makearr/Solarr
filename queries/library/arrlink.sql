-- name: UpsertArrLink :exec
INSERT INTO arr_link (instance_id, source_kind, source_id, local_kind, local_id) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (instance_id, source_kind, source_id) DO UPDATE SET local_kind = excluded.local_kind, local_id = excluded.local_id;

-- name: SetArrLinkSynced :exec
UPDATE arr_link SET synced_monitored = ? WHERE instance_id = ? AND source_kind = ? AND source_id = ?;

-- name: ListArrLinksForInstance :many
SELECT source_kind, source_id, local_kind, local_id, synced_monitored FROM arr_link WHERE instance_id = ? ORDER BY source_kind, source_id;

-- name: DeleteArrLinksForInstance :exec
DELETE FROM arr_link WHERE instance_id = ?;
