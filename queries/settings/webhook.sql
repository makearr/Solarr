-- Outgoing webhooks. Column lists follow table order.

-- name: ListWebhooks :many
SELECT id, name, enabled, preset, url, secret, events, category_ids, include_paths, include_adult, last_status, last_error, last_at, created_at
FROM webhook ORDER BY id;

-- name: GetWebhook :one
SELECT id, name, enabled, preset, url, secret, events, category_ids, include_paths, include_adult, last_status, last_error, last_at, created_at
FROM webhook WHERE id = ?;

-- name: InsertWebhook :one
INSERT INTO webhook (name, enabled, preset, url, secret, events, category_ids, include_paths, include_adult, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id;

-- name: UpdateWebhook :exec
UPDATE webhook SET name = ?, enabled = ?, preset = ?, url = ?, secret = ?, events = ?, category_ids = ?, include_paths = ?, include_adult = ? WHERE id = ?;

-- name: DeleteWebhook :exec
DELETE FROM webhook WHERE id = ?;

-- name: SetWebhookResult :exec
UPDATE webhook SET last_status = ?, last_error = ?, last_at = ? WHERE id = ?;

-- name: SetWebhookEnabled :exec
UPDATE webhook SET enabled = ? WHERE id = ?;
