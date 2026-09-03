-- Column lists follow table order, or sqlc stops reusing the generated model.

-- name: GetCachedMetadata :one
SELECT kind, external_id, source, body, fetched_at
FROM metadata_cache WHERE kind = ? AND external_id = ?;

-- name: SetCachedMetadata :exec
INSERT INTO metadata_cache (kind, external_id, source, body, fetched_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (kind, external_id) DO UPDATE SET
  source = excluded.source,
  body = excluded.body,
  fetched_at = excluded.fetched_at;
