-- Indexer connections (stage 5, ADR-37/43/47). Column lists follow table order.

-- name: CreateIndexer :one
INSERT INTO indexer (name, impl, base_url, api_key, enabled, priority, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, impl, base_url, api_key, enabled, priority, categories_json,
          caps_fetched_at, settings_json, created_at, updated_at;

-- name: GetIndexer :one
SELECT id, name, impl, base_url, api_key, enabled, priority, categories_json,
       caps_fetched_at, settings_json, created_at, updated_at
FROM indexer WHERE id = ?;

-- name: ListIndexers :many
SELECT id, name, impl, base_url, api_key, enabled, priority, categories_json,
       caps_fetched_at, settings_json, created_at, updated_at
FROM indexer ORDER BY priority, name;

-- name: UpdateIndexer :exec
UPDATE indexer
SET name = ?, impl = ?, base_url = ?, enabled = ?, priority = ?, settings_json = ?, updated_at = ?
WHERE id = ?;

-- A separate statement from UpdateIndexer, the same reasoning arr_instance's own
-- SetArrInstanceAPIKey gives: Seal generates a fresh nonce every call, so writing it back
-- unconditionally on every unrelated edit would be silent, pointless churn.
-- name: SetIndexerAPIKey :exec
UPDATE indexer SET api_key = ?, updated_at = ? WHERE id = ?;

-- name: SetIndexerCaps :exec
UPDATE indexer SET categories_json = ?, caps_fetched_at = ?, updated_at = ? WHERE id = ?;

-- name: DeleteIndexer :exec
DELETE FROM indexer WHERE id = ?;
