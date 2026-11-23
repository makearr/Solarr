-- Incoming webhooks. Column lists follow table order.

-- name: ListWebhookInbound :many
SELECT id, name, enabled, token_hash, actions, category_ids, media_server_id, last_used_at, created_at
FROM webhook_inbound ORDER BY id;

-- name: GetWebhookInbound :one
SELECT id, name, enabled, token_hash, actions, category_ids, media_server_id, last_used_at, created_at
FROM webhook_inbound WHERE id = ?;

-- name: InsertWebhookInbound :one
INSERT INTO webhook_inbound (name, enabled, token_hash, actions, category_ids, media_server_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id;

-- name: UpdateWebhookInbound :exec
UPDATE webhook_inbound SET name = ?, enabled = ?, actions = ?, category_ids = ?, media_server_id = ? WHERE id = ?;

-- name: SetWebhookInboundToken :exec
UPDATE webhook_inbound SET token_hash = ? WHERE id = ?;

-- name: TouchWebhookInbound :exec
UPDATE webhook_inbound SET last_used_at = ? WHERE id = ?;

-- name: DeleteWebhookInbound :exec
DELETE FROM webhook_inbound WHERE id = ?;
