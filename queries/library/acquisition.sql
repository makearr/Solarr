-- Candidates, decisions and provenance. Column lists follow table order.

-- name: UpsertCandidate :one
INSERT INTO candidate (indexer_id, guid, title, attributes_id, size_bytes, seeders,
                       leechers, published_at, protocol, download_url,
                       parse_confidence, source, first_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (indexer_id, guid) DO UPDATE SET
  title = excluded.title,
  seeders = excluded.seeders,
  leechers = excluded.leechers,
  expires_at = excluded.expires_at
RETURNING id, indexer_id, guid, title, attributes_id, size_bytes, seeders, leechers,
          published_at, protocol, download_url, parse_confidence, source,
          first_seen_at, expires_at;

-- name: GetCandidate :one
SELECT id, indexer_id, guid, title, attributes_id, size_bytes, seeders, leechers,
       published_at, protocol, download_url, parse_confidence, source,
       first_seen_at, expires_at
FROM candidate WHERE id = ?;

-- A DEFER is not a failure: the candidate stays cached until it expires (section 10.1).
-- name: DeleteExpiredCandidates :exec
DELETE FROM candidate WHERE expires_at <= ?;

-- name: RecordDecision :one
INSERT INTO candidate_decision (candidate_id, desired_item_id, outcome, score,
                                components_json, rejections_json, deferrals_json,
                                match_confidence, decided_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, candidate_id, desired_item_id, outcome, score, components_json,
          rejections_json, deferrals_json, match_confidence, decided_at;

-- Every candidate with its full breakdown, newest first (F1, F16).
-- name: ListDecisionsForItem :many
SELECT id, candidate_id, desired_item_id, outcome, score, components_json,
       rejections_json, deferrals_json, match_confidence, decided_at
FROM candidate_decision
WHERE desired_item_id = ? ORDER BY decided_at DESC, id DESC LIMIT ?;

-- name: CreateAcquisition :one
INSERT INTO acquisition (desired_item_id, candidate_id, indexer_id, candidate_guid,
                         candidate_title, search_terms, downloader_id,
                         download_client_ref, download_dir, client_category,
                         client_fingerprint, protocol, ddl_resolved_url,
                         ddl_bytes_done, ddl_resumable, state, last_error,
                         created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, desired_item_id, candidate_id, indexer_id, candidate_guid,
          candidate_title, search_terms, downloader_id, download_client_ref,
          download_dir, client_category, client_fingerprint, protocol,
          ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
          created_at, updated_at;

-- name: GetAcquisition :one
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid,
       candidate_title, search_terms, downloader_id, download_client_ref,
       download_dir, client_category, client_fingerprint, protocol,
       ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
       created_at, updated_at
FROM acquisition WHERE id = ?;

-- Startup reconciliation polls every non-terminal acquisition (section 10.9).
-- name: ListActiveAcquisitions :many
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid,
       candidate_title, search_terms, downloader_id, download_client_ref,
       download_dir, client_category, client_fingerprint, protocol,
       ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
       created_at, updated_at
FROM acquisition
WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED')
ORDER BY id;

-- Identity is the client reference; the path is re-read on every poll.
-- name: GetAcquisitionByClientRef :one
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid,
       candidate_title, search_terms, downloader_id, download_client_ref,
       download_dir, client_category, client_fingerprint, protocol,
       ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error,
       created_at, updated_at
FROM acquisition WHERE downloader_id = ? AND download_client_ref = ?;

-- name: SetAcquisitionState :exec
UPDATE acquisition SET state = ?, last_error = ?, updated_at = ? WHERE id = ?;

-- name: SetAcquisitionPath :exec
UPDATE acquisition SET download_dir = ?, updated_at = ? WHERE id = ?;

-- Persisted before bytes move, so a restart resumes rather than restarts (ADR-46).
-- name: SetDownloadProgress :exec
UPDATE acquisition
SET ddl_resolved_url = ?, ddl_bytes_done = ?, ddl_resumable = ?, updated_at = ?
WHERE id = ?;

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

-- name: RecordImportDecision :one
INSERT INTO import_decision (desired_item_id, acquisition_id, incumbent_object_id,
                             arrived_probe_json, comparison_json, outcome,
                             reason_json, decided_at, overridden_by, overridden_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, desired_item_id, acquisition_id, incumbent_object_id,
          arrived_probe_json, comparison_json, outcome, reason_json, decided_at,
          overridden_by, overridden_at;

-- name: ListImportDecisionsNeedingReview :many
SELECT id, desired_item_id, acquisition_id, incumbent_object_id,
       arrived_probe_json, comparison_json, outcome, reason_json, decided_at,
       overridden_by, overridden_at
FROM import_decision
WHERE outcome = 'NEEDS_REVIEW' ORDER BY decided_at DESC LIMIT ?;

-- name: Quarantine :one
INSERT INTO quarantine (original_path, quarantine_path, root_folder_id,
                        media_object_id, acquisition_id, reason, size_bytes,
                        quarantined_at, expires_at, restored_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, original_path, quarantine_path, root_folder_id, media_object_id,
          acquisition_id, reason, size_bytes, quarantined_at, expires_at, restored_at;

-- name: MarkQuarantineRestored :exec
UPDATE quarantine SET restored_at = ? WHERE id = ?;

-- name: DeleteQuarantineEntry :exec
DELETE FROM quarantine WHERE id = ?;

-- Quarantined bytes count against the free-space gate, so they are summed per
-- root rather than assumed to be free (ADR-12).
-- name: QuarantinedBytesForRoot :one
SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS held FROM quarantine
WHERE root_folder_id = ? AND restored_at IS NULL;

-- name: ListExpiredQuarantine :many
SELECT id, original_path, quarantine_path, root_folder_id, media_object_id,
       acquisition_id, reason, size_bytes, quarantined_at, expires_at, restored_at
FROM quarantine
WHERE restored_at IS NULL AND expires_at <= ? ORDER BY expires_at LIMIT ?;

-- name: RecordAcquiredFile :one
INSERT INTO acquisition_file (acquisition_id, source_path, media_object_id,
                              library_path_id, imported_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, acquisition_id, source_path, media_object_id, library_path_id, imported_at;

-- name: ListAcquiredFiles :many
SELECT id, acquisition_id, source_path, media_object_id, library_path_id, imported_at
FROM acquisition_file WHERE acquisition_id = ? ORDER BY id;

-- Which search caused this file, and which download produced it (F4, section 12).
-- name: ProvenanceForObject :many
SELECT a.id, a.desired_item_id, a.candidate_guid, a.candidate_title, a.search_terms,
       a.indexer_id, a.protocol, a.created_at
FROM acquisition a
JOIN acquisition_file f ON f.acquisition_id = a.id
WHERE f.media_object_id = ? ORDER BY a.created_at DESC;
