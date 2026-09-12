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

-- Invariant 83 (todo 4.15/4.8): "a blocked hash found later by a scan is
-- reported, never touched" -- driven from blocked_content (small, user-sized)
-- rather than media_object (library-sized), using the same two indexed exact
-- hashes FindBlockedContentByExactHash checks at import time, just in the
-- opposite direction. Every library_path, not only the primary one: ADR-11
-- cross-entry hardlinking means a blocked hash can occupy more than one path,
-- and a health report exists to be acted on, not to under-count. Known,
-- narrow gap: the cursor is bc.id, so a page boundary landing mid-way
-- through one block's own several paths skips the remainder on the next
-- page -- acceptable today since a block rarely has more than a handful of
-- present copies, revisit if that stops being true.
-- name: ListBlockedContentPresentInLibrary :many
SELECT bc.id AS blocked_content_id, bc.reason, bc.title, mo.id AS media_object_id,
       lp.root_folder_id, lp.rel_path
FROM blocked_content bc
JOIN media_object mo ON (bc.oshash IS NOT NULL AND mo.oshash = bc.oshash)
                     OR (bc.quick_hash IS NOT NULL AND mo.quick_hash = bc.quick_hash)
JOIN library_path lp ON lp.media_object_id = mo.id
WHERE bc.id > sqlc.arg(after_id)
ORDER BY bc.id, lp.id
LIMIT sqlc.arg(page_size);
