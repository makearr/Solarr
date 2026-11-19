-- Column lists follow table order, or sqlc stops reusing the generated model.

-- name: GetCachedMetadata :one
SELECT kind, external_id, source, body, fetched_at, body_bytes
FROM metadata_cache WHERE kind = ? AND external_id = ?;

-- name: SetCachedMetadata :exec
INSERT INTO metadata_cache (kind, external_id, source, body, fetched_at, body_bytes)
VALUES (sqlc.arg(kind), sqlc.arg(external_id), sqlc.arg(source), sqlc.arg(body), sqlc.arg(fetched_at), LENGTH(CAST(sqlc.arg(body) AS BLOB)))
ON CONFLICT (kind, external_id) DO UPDATE SET
  source = excluded.source,
  body = excluded.body,
  fetched_at = excluded.fetched_at,
  body_bytes = excluded.body_bytes;

-- ADR-60's budget: bytes stored (body_bytes is a BLOB's length, not characters), not the file, which DELETE does not shrink.
-- A covering scan of the age index: no body is read.
-- name: SumMetadataCacheBytes :one
SELECT CAST(COALESCE(SUM(body_bytes), 0) AS INTEGER) FROM metadata_cache INDEXED BY metadata_cache_by_age;

-- name: CountMetadataCache :one
SELECT CAST(COUNT(*) AS INTEGER) FROM metadata_cache;

-- ADR-60's max_age: a per-entry ceiling, enforced regardless of budget.
-- A chunk at a time, oldest first, so no transaction holds the writer for long.
-- name: DeleteMetadataCacheOlderThan :execrows
DELETE FROM metadata_cache WHERE (kind, external_id) IN (
  SELECT old.kind, old.external_id FROM metadata_cache old WHERE old.fetched_at < ? ORDER BY old.fetched_at ASC LIMIT ?
);

-- ADR-60's budget-triggered eviction: oldest first, exactly n rows, returning each one's bytes so the caller keeps a
-- running total instead of summing the whole cache again. WITHOUT ROWID, so the subquery is by primary key.
-- name: DeleteOldestMetadataCache :many
DELETE FROM metadata_cache WHERE (kind, external_id) IN (
  SELECT kind, external_id FROM metadata_cache ORDER BY fetched_at ASC LIMIT ?
)
RETURNING body_bytes AS bytes;

-- The manual "always safe" clear (Invariant 5).
-- name: ClearMetadataCache :exec
DELETE FROM metadata_cache;
