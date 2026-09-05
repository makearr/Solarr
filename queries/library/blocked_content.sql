-- ADR-53 part 2 (migration 0019). Column lists follow table order, or sqlc
-- stops reusing models.

-- name: CreateBlockedContent :one
INSERT INTO blocked_content (oshash, quick_hash, chromaprint, phash, size_bytes, reason,
                              note, media_type, title, release_group, source, user_id,
                              expires_at, created_at)
VALUES (sqlc.arg(oshash), sqlc.arg(quick_hash), sqlc.arg(chromaprint), sqlc.arg(phash),
        sqlc.arg(size_bytes), sqlc.arg(reason), sqlc.arg(note), sqlc.arg(media_type),
        sqlc.arg(title), sqlc.arg(release_group), sqlc.arg(source), sqlc.arg(user_id),
        sqlc.arg(expires_at), sqlc.arg(created_at))
RETURNING id, oshash, quick_hash, chromaprint, phash, size_bytes, reason, note, media_type,
          title, release_group, source, user_id, expires_at, created_at, hit_count, last_hit_at;

-- name: GetBlockedContent :one
SELECT id, oshash, quick_hash, chromaprint, phash, size_bytes, reason, note, media_type,
       title, release_group, source, user_id, expires_at, created_at, hit_count, last_hit_at
FROM blocked_content WHERE id = ?;

-- The import gate's own check (section 10.3 step 3, ADR-53): exact hashes
-- only, oshash first since it is the stronger identifier when both exist.
-- Perceptual columns (chromaprint/phash) are matched separately once
-- fingerprinting exists -- a fuzzy hit is never folded into this exact check
-- (architecture.md section 5.8's own "never treated as an exact one" rule).
-- name: FindBlockedContentByExactHash :many
SELECT id, oshash, quick_hash, chromaprint, phash, size_bytes, reason, note, media_type,
       title, release_group, source, user_id, expires_at, created_at, hit_count, last_hit_at
FROM blocked_content
WHERE (expires_at IS NULL OR expires_at > sqlc.arg(now))
  AND ((oshash IS NOT NULL AND oshash = sqlc.arg(oshash))
       OR (quick_hash IS NOT NULL AND quick_hash = sqlc.arg(quick_hash)));

-- name: ListBlockedContent :many
SELECT id, oshash, quick_hash, chromaprint, phash, size_bytes, reason, note, media_type,
       title, release_group, source, user_id, expires_at, created_at, hit_count, last_hit_at
FROM blocked_content WHERE id > sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(page_size);

-- name: RecordBlockedContentHit :exec
UPDATE blocked_content SET hit_count = hit_count + 1, last_hit_at = ? WHERE id = ?;

-- Manual blocks never expire (section 10.8); auto entries may, matching
-- section 10.11's existing blocklisted_release rule.
-- name: PurgeExpiredBlockedContent :exec
DELETE FROM blocked_content WHERE expires_at IS NOT NULL AND expires_at <= ?;

-- name: RemoveBlockedContent :exec
DELETE FROM blocked_content WHERE id = ?;
