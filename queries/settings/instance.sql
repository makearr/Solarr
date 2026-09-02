-- Instance-wide policy, seeded from config.yml and changeable afterwards (ADR-21).

-- name: ListInstanceSettings :many
SELECT key, value_json, updated_at FROM instance_setting ORDER BY key;

-- name: GetInstanceSetting :one
SELECT key, value_json, updated_at FROM instance_setting WHERE key = ?;

-- name: SetInstanceSetting :exec
INSERT INTO instance_setting (key, value_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
  value_json = excluded.value_json,
  updated_at = excluded.updated_at;
