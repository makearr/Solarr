-- Transcode setups per target quality and the per-library switch, priority and order.

-- name: GetTranscodeSetup :one
SELECT target_id, revision, setup_json, updated_at FROM transcode_setup WHERE target_id = ?;

-- name: ListTranscodeSetups :many
SELECT target_id, revision, setup_json, updated_at FROM transcode_setup ORDER BY target_id;

-- name: UpsertTranscodeSetup :exec
INSERT INTO transcode_setup (target_id, revision, setup_json, updated_at) VALUES (?, 1, ?, ?)
ON CONFLICT (target_id) DO UPDATE SET revision = transcode_setup.revision + 1, setup_json = excluded.setup_json, updated_at = excluded.updated_at;

-- name: ListCategoryTranscode :many
SELECT category_id, enabled, priority, list_order FROM category_transcode ORDER BY category_id;

-- name: UpsertCategoryTranscode :exec
INSERT INTO category_transcode (category_id, enabled, priority, list_order) VALUES (?, ?, ?, ?)
ON CONFLICT (category_id) DO UPDATE SET enabled = excluded.enabled, priority = excluded.priority, list_order = excluded.list_order;
