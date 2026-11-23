-- Webhook deliveries to make. Column lists follow table order.

-- name: AddWebhookPending :exec
INSERT INTO webhook_pending (webhook_id, event, payload, queued_at, attempts, next_at)
VALUES (?, ?, ?, ?, 0, ?);

-- name: ListDueWebhookPending :many
SELECT id, webhook_id, event, payload, queued_at, attempts, next_at FROM webhook_pending
WHERE next_at <= ? ORDER BY id LIMIT ?;

-- name: DeleteWebhookPending :exec
DELETE FROM webhook_pending WHERE id = ?;

-- name: RetryWebhookPending :exec
UPDATE webhook_pending SET attempts = attempts + 1, next_at = ? WHERE id = ?;

-- name: DeleteWebhookPendingOf :exec
DELETE FROM webhook_pending WHERE webhook_id = ?;

-- name: CountWebhookPending :one
SELECT CAST(COUNT(*) AS INTEGER) AS pending, CAST(COALESCE(MIN(next_at), 0) AS INTEGER) AS next_at FROM webhook_pending WHERE webhook_id = ?;
