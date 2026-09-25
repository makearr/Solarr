-- Download client connections (stage 5, ADR-46/57). Column lists follow table order.

-- name: CreateDownloader :one
INSERT INTO downloader (name, impl, base_url, credentials, enabled, protocol, settings_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, impl, base_url, credentials, enabled, protocol, settings_json,
          created_at, updated_at;

-- name: GetDownloader :one
SELECT id, name, impl, base_url, credentials, enabled, protocol, settings_json,
       created_at, updated_at
FROM downloader WHERE id = ?;

-- name: ListDownloaders :many
SELECT id, name, impl, base_url, credentials, enabled, protocol, settings_json,
       created_at, updated_at
FROM downloader ORDER BY name;

-- name: UpdateDownloader :exec
UPDATE downloader
SET name = ?, impl = ?, base_url = ?, enabled = ?, protocol = ?, settings_json = ?, updated_at = ?
WHERE id = ?;

-- A separate statement from UpdateDownloader, the same reasoning indexer's own
-- SetIndexerAPIKey gives: Seal generates a fresh nonce every call, so writing it back
-- unconditionally on every unrelated edit would be silent, pointless churn.
-- name: SetDownloaderCredentials :exec
UPDATE downloader SET credentials = ?, updated_at = ? WHERE id = ?;

-- name: DeleteDownloader :exec
DELETE FROM downloader WHERE id = ?;
