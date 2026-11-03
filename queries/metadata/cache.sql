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

-- ADR-60's budget: bytes stored (a BLOB's length; a text's counts characters), not the file, which DELETE does not shrink.
-- name: SumMetadataCacheBytes :one
SELECT CAST(COALESCE(SUM(LENGTH(CAST(body AS BLOB))), 0) AS INTEGER) FROM metadata_cache;

-- name: CountMetadataCache :one
SELECT CAST(COUNT(*) AS INTEGER) FROM metadata_cache;

-- ADR-60's max_age: a per-entry ceiling, enforced regardless of budget.
-- name: DeleteMetadataCacheOlderThan :execrows
DELETE FROM metadata_cache WHERE fetched_at < ?;

-- ADR-60's budget-triggered eviction: oldest first, exactly n rows, returning each one's bytes so the caller keeps a
-- running total instead of summing the whole cache again. WITHOUT ROWID, so the subquery is by primary key.
-- name: DeleteOldestMetadataCache :many
DELETE FROM metadata_cache WHERE (kind, external_id) IN (
  SELECT kind, external_id FROM metadata_cache ORDER BY fetched_at ASC LIMIT ?
)
RETURNING CAST(LENGTH(CAST(body AS BLOB)) AS INTEGER) AS bytes;

-- The manual "always safe" clear (Invariant 5).
-- name: ClearMetadataCache :exec
DELETE FROM metadata_cache;
