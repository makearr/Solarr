-- Candidates, decisions and provenance. Column lists follow table order.

-- name: CreateAcquisition :one
INSERT INTO acquisition (desired_item_id, candidate_id, indexer_id, candidate_guid,
                         candidate_title, search_terms, downloader_id,
                         download_client_ref, download_dir, client_category,
                         client_fingerprint, protocol, ddl_resolved_url,
                         ddl_bytes_done, ddl_resumable, state, last_error,
                         created_at, updated_at, content_hash, size_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, desired_item_id, candidate_id, indexer_id, candidate_guid,
          candidate_title, search_terms, downloader_id, download_client_ref,
          download_dir, client_category, client_fingerprint, protocol,
          ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
          created_at, updated_at, content_hash, size_bytes;

-- name: GetAcquisition :one
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid,
       candidate_title, search_terms, downloader_id, download_client_ref,
       download_dir, client_category, client_fingerprint, protocol,
       ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
       created_at, updated_at, content_hash, size_bytes
FROM acquisition WHERE id = ?;

-- Checked before CreateAcquisition so a caller gets a clean, mapped refusal
-- rather than the acquisition_one_inflight unique index's own raw
-- constraint error (Invariant 15) -- the same "check first" shape
-- CreateCategory's own category(media_type, name) guard already uses.
-- name: GetActiveAcquisitionForItem :one
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid,
       candidate_title, search_terms, downloader_id, download_client_ref,
       download_dir, client_category, client_fingerprint, protocol,
       ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
       created_at, updated_at, content_hash, size_bytes
FROM acquisition
WHERE desired_item_id = ? AND state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');

-- Startup reconciliation polls every non-terminal acquisition (section 10.9).
-- name: ListActiveAcquisitions :many
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid,
       candidate_title, search_terms, downloader_id, download_client_ref,
       download_dir, client_category, client_fingerprint, protocol,
       ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
       created_at, updated_at, content_hash, size_bytes
FROM acquisition
WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED')
ORDER BY id;

-- Imported downloads still in their client: kept there to seed until a client's goal is met.
-- name: ListImportedAcquisitions :many
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid,
       candidate_title, search_terms, downloader_id, download_client_ref,
       download_dir, client_category, client_fingerprint, protocol,
       ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
       created_at, updated_at, content_hash, size_bytes
FROM acquisition
WHERE state = 'IMPORTED'
ORDER BY id;

-- A client's queue as Solarr fills it: sent and not finished downloading (ADR-57).
-- name: CountQueuedOnDownloader :one
SELECT CAST(COUNT(*) AS INTEGER) FROM acquisition
WHERE downloader_id = ? AND state IN ('QUEUED', 'DOWNLOADING');

-- name: SetAcquisitionState :exec
UPDATE acquisition SET state = ?, last_error = ?, updated_at = ? WHERE id = ?;

-- name: SetAcquisitionPath :exec
UPDATE acquisition SET download_dir = ?, updated_at = ? WHERE id = ?;

-- name: BlocklistRelease :one
INSERT INTO blocklisted_release (indexer_id, guid, content_hash, title,
                                 desired_item_id, reason, detail_json, source,
                                 expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, indexer_id, guid, content_hash, title, desired_item_id, reason,
          detail_json, source, expires_at, created_at;

-- The pipeline filters against this between dedupe and evaluate (section 10.11).
-- name: IsBlocklisted :one
SELECT COUNT(*) AS blocked FROM blocklisted_release
WHERE (expires_at IS NULL OR expires_at > sqlc.arg(now))
  AND ((indexer_id = sqlc.arg(indexer_id) AND guid = sqlc.arg(guid))
       OR (content_hash IS NOT NULL AND content_hash = sqlc.arg(content_hash)));

-- name: ListBlocklist :many
SELECT id, indexer_id, guid, content_hash, title, desired_item_id, reason,
       detail_json, source, expires_at, created_at
FROM blocklisted_release
WHERE id > sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(page_size);

-- name: RemoveFromBlocklist :exec
DELETE FROM blocklisted_release WHERE id = ?;

-- Auto entries expire because indexers repair things; manual ones do not.
-- name: PurgeExpiredBlocklist :exec
DELETE FROM blocklisted_release WHERE expires_at IS NOT NULL AND expires_at <= ?;

-- name: RecycleBin :one
INSERT INTO recycle_bin (original_path, recycled_path, root_folder_id,
                        media_object_id, acquisition_id, reason, size_bytes,
                        recycled_at, expires_at, restored_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, original_path, recycled_path, root_folder_id, media_object_id,
          acquisition_id, reason, size_bytes, recycled_at, expires_at, restored_at;

-- :execrows so a caller can tell "already restored" (0 rows) from "restored
-- just now" (1 row) without a separate read first -- nothing calls this yet,
-- so tightening it here does not touch a working caller.
-- name: MarkRecycleBinRestored :execrows
UPDATE recycle_bin SET restored_at = ? WHERE id = ? AND restored_at IS NULL;

-- name: DeleteRecycleBinEntry :exec
DELETE FROM recycle_bin WHERE id = ?;

-- Recycled bytes count against the free-space gate, so they are summed per
-- root rather than assumed to be free (ADR-12).
-- name: RecycledBytesForRoot :one
SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS held FROM recycle_bin
WHERE root_folder_id = ? AND restored_at IS NULL;

-- name: ListExpiredRecycleBin :many
SELECT id, original_path, recycled_path, root_folder_id, media_object_id,
       acquisition_id, reason, size_bytes, recycled_at, expires_at, restored_at
FROM recycle_bin
WHERE restored_at IS NULL AND expires_at <= ? ORDER BY expires_at LIMIT ?;

-- name: GetRecycleBin :one
SELECT id, original_path, recycled_path, root_folder_id, media_object_id,
       acquisition_id, reason, size_bytes, recycled_at, expires_at, restored_at
FROM recycle_bin WHERE id = ?;

-- The global counterpart to RecycledBytesForRoot, for the config.max_bytes
-- guard (section 10.8) -- a budget across every root, not one at a time.
-- name: SumRecycledBytes :one
SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS held FROM recycle_bin
WHERE restored_at IS NULL;

-- Oldest-first eviction order for the max_bytes guard -- distinct from
-- ListExpiredRecycleBin, which is age-of-expiry, not age-of-entry.
-- name: ListActiveRecycleBinOldestFirst :many
SELECT id, original_path, recycled_path, root_folder_id, media_object_id,
       acquisition_id, reason, size_bytes, recycled_at, expires_at, restored_at
FROM recycle_bin
WHERE restored_at IS NULL ORDER BY recycled_at LIMIT ?;

-- name: CountRecycleBin :one
SELECT CAST(COUNT(*) AS INTEGER) AS held FROM recycle_bin WHERE restored_at IS NULL;

-- name: ListActiveRecycleBinNewestFirst :many
SELECT id, original_path, recycled_path, root_folder_id, media_object_id,
       acquisition_id, reason, size_bytes, recycled_at, expires_at, restored_at
FROM recycle_bin
WHERE restored_at IS NULL ORDER BY recycled_at DESC, id DESC LIMIT ?;

-- The per-root counterpart, for the max_percent_of_root guard.
-- name: ListActiveRecycleBinByRootOldestFirst :many
SELECT id, original_path, recycled_path, root_folder_id, media_object_id,
       acquisition_id, reason, size_bytes, recycled_at, expires_at, restored_at
FROM recycle_bin
WHERE restored_at IS NULL AND root_folder_id = sqlc.arg(root_folder_id)
ORDER BY recycled_at LIMIT sqlc.arg(limit);

-- name: RecordAcquiredFile :one
INSERT INTO acquisition_file (acquisition_id, source_path, media_object_id,
                              library_path_id, imported_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, acquisition_id, source_path, media_object_id, library_path_id, imported_at;

-- Which search caused this file, and which download produced it (F4, section 12).
-- name: ProvenanceForObject :many
SELECT a.id, a.desired_item_id, a.candidate_guid, a.candidate_title, a.search_terms,
       a.indexer_id, a.protocol, a.created_at
FROM acquisition a
JOIN acquisition_file f ON f.acquisition_id = a.id
WHERE f.media_object_id = ? ORDER BY a.created_at DESC;

-- The client's own name and size, recorded when first seen (section 10.9's fingerprint).
-- name: SetAcquisitionFingerprint :exec
UPDATE acquisition SET client_fingerprint = ?, updated_at = ? WHERE id = ?;

-- What a library's downloads still on their way will take when they land, so the free-space gate
-- does not count the same room twice between capacity measurements (ADR-12).
-- name: InFlightBytesForCategory :one
SELECT CAST(COALESCE(SUM(a.size_bytes), 0) AS INTEGER) AS total
FROM acquisition a
JOIN desired_item di ON di.id = a.desired_item_id
WHERE di.category_id = ? AND a.state IN ('QUEUED', 'DOWNLOADING', 'COMPLETED', 'IMPORTING');

-- The largest release of the item that failed since the cutoff; an automatic retry may not jump far above it.
-- name: LargestFailedBytesForItem :one
SELECT CAST(COALESCE(MAX(size_bytes), 0) AS INTEGER) AS largest
FROM acquisition
WHERE desired_item_id = ? AND state = 'FAILED' AND updated_at >= ?;

-- Retention: decisions age into decision_rollup, then go; a candidate goes once nothing refers to it.

-- name: GetAcquisitionStall :one
SELECT acquisition_id, strikes, left_bytes, progress_at, checked_at, importing_finished FROM acquisition_stall WHERE acquisition_id = ?;

-- name: SetAcquisitionStall :exec
INSERT INTO acquisition_stall (acquisition_id, strikes, left_bytes, progress_at, checked_at, importing_finished)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (acquisition_id) DO UPDATE SET
  strikes = excluded.strikes, left_bytes = excluded.left_bytes,
  progress_at = excluded.progress_at, checked_at = excluded.checked_at, importing_finished = excluded.importing_finished;

-- name: DeleteAcquisitionStall :exec
DELETE FROM acquisition_stall WHERE acquisition_id = ?;
