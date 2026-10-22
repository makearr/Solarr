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

-- ADR-60's budget-triggered eviction: oldest first, exactly n rows so the
-- caller can re-measure and stop once it is back under budget rather than
-- guessing a cutoff time. metadata_cache is WITHOUT ROWID (its own
-- (kind, external_id) primary key already is the index), so the row-value
-- form is used instead of a rowid subquery.
-- name: DeleteOldestMetadataCache :execrows
DELETE FROM metadata_cache WHERE (kind, external_id) IN (
  SELECT kind, external_id FROM metadata_cache ORDER BY fetched_at ASC LIMIT ?
);

-- The manual "always safe" clear (Invariant 5).
-- name: ClearMetadataCache :exec
DELETE FROM metadata_cache;
