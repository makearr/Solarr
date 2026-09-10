-- ADR-48 (migration 0018). Column lists follow table order, or sqlc stops
-- reusing models.

-- name: CreateImportSession :one
INSERT INTO import_session (source_path, origin, acquisition_id, category_id, state,
                             threshold, started_at)
VALUES (sqlc.arg(source_path), sqlc.arg(origin), sqlc.arg(acquisition_id),
        sqlc.arg(category_id), 'INSPECTING', sqlc.arg(threshold), sqlc.arg(started_at))
RETURNING id, source_path, origin, acquisition_id, category_id, state, threshold,
          units_total, units_committed, units_queued, bytes_imported, extract_dir,
          started_at, finished_at, error, units_skipped, units_failed;

-- name: GetImportSession :one
SELECT id, source_path, origin, acquisition_id, category_id, state, threshold,
       units_total, units_committed, units_queued, bytes_imported, extract_dir,
       started_at, finished_at, error, units_skipped, units_failed
FROM import_session WHERE id = ?;

-- Small, operator-facing list -- session counts never approach library scale
-- (Invariant 22 is about files, not import runs), so this is a plain list
-- rather than cursor-paginated, newest first per idx_import_session_state.
-- filter_by_state=0 means unfiltered, the same two-argument shape ListUnidentified
-- already uses -- an "arg = '' OR ..." single-argument form leaves sqlc unable to
-- infer a concrete type for the argument.
-- name: ListImportSessions :many
SELECT id, source_path, origin, acquisition_id, category_id, state, threshold,
       units_total, units_committed, units_queued, bytes_imported, extract_dir,
       started_at, finished_at, error, units_skipped, units_failed
FROM import_session
WHERE sqlc.arg(filter_by_state) = 0 OR state = sqlc.arg(state)
ORDER BY started_at DESC;

-- name: SetImportSessionExtractDir :exec
UPDATE import_session SET extract_dir = ? WHERE id = ?;

-- name: FinishImportSession :exec
UPDATE import_session SET state = sqlc.arg(state), finished_at = sqlc.arg(finished_at),
                           error = sqlc.arg(error)
WHERE id = sqlc.arg(id);

-- Only from INSPECTING: refuses a no-op cancel of an already-terminal session
-- and never overwrites CANCELLING with itself. :execrows lets the caller
-- distinguish "already stopped/never running" (0 rows) from "now stopping".
-- name: RequestImportSessionCancel :execrows
UPDATE import_session SET state = 'CANCELLING'
WHERE id = sqlc.arg(id) AND state = 'INSPECTING';

-- Recomputed from import_unit rather than incremented as units are added or
-- redecided -- the same drift-and-repair shape CategoryCounters already uses,
-- cheaper to reason about than keeping a running total consistent through
-- every future call site that can change a unit's decision.
-- name: RecomputeImportSessionCounts :exec
UPDATE import_session SET
  units_total = (SELECT COUNT(*) FROM import_unit iu WHERE iu.session_id = sqlc.arg(session_id)),
  units_committed = (SELECT COUNT(*) FROM import_unit iu
                      WHERE iu.session_id = sqlc.arg(session_id) AND iu.decision = 'COMMIT'),
  units_queued = (SELECT COUNT(*) FROM import_unit iu
                   WHERE iu.session_id = sqlc.arg(session_id) AND iu.decision = 'QUEUE'),
  units_skipped = (SELECT COUNT(*) FROM import_unit iu
                     WHERE iu.session_id = sqlc.arg(session_id) AND iu.decision = 'SKIP'),
  units_failed = (SELECT COUNT(*) FROM import_unit iu
                    WHERE iu.session_id = sqlc.arg(session_id) AND iu.decision = 'FAILED'),
  bytes_imported = (SELECT COALESCE(SUM(iu.bytes), 0) FROM import_unit iu
                      WHERE iu.session_id = sqlc.arg(session_id) AND iu.decision = 'COMMIT')
WHERE id = sqlc.arg(session_id);

-- name: InsertImportUnit :one
INSERT INTO import_unit (session_id, rel_path, shape, file_count, bytes, confidence,
                          decision, work_id, edition_id, evidence_json, candidates_json,
                          reason)
VALUES (sqlc.arg(session_id), sqlc.arg(rel_path), sqlc.arg(shape), sqlc.arg(file_count),
        sqlc.arg(bytes), sqlc.arg(confidence), sqlc.arg(decision), sqlc.arg(work_id),
        sqlc.arg(edition_id), sqlc.arg(evidence_json), sqlc.arg(candidates_json),
        sqlc.arg(reason))
RETURNING id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
          work_id, edition_id, evidence_json, candidates_json, reason;

-- name: ListImportUnits :many
SELECT id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
       work_id, edition_id, evidence_json, candidates_json, reason
FROM import_unit WHERE session_id = ? ORDER BY id;

-- Cursor-paginated, unlike ListImportUnits above: a session's own unit count
-- is not bounded the way a session count is (Invariant 22 is exactly the
-- "500k+ files" case a whole-library adoption run can hit), so the API layer
-- needs a real page, not the whole session's units in one response.
-- ListImportUnits itself stays as-is for the job's own small, in-process needs
-- (resumeOrCreate's "the last unit committed so far" lookup).
-- name: ListImportUnitsPage :many
SELECT id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
       work_id, edition_id, evidence_json, candidates_json, reason
FROM import_unit
WHERE session_id = sqlc.arg(session_id) AND id > sqlc.arg(after_id)
  AND (sqlc.arg(filter_by_decision) = 0 OR decision = sqlc.arg(decision))
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: GetImportUnit :one
SELECT id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
       work_id, edition_id, evidence_json, candidates_json, reason
FROM import_unit WHERE id = ?;

-- The only mutation IDENTIFY needs once it exists: a unit starts QUEUE with no
-- identity (F29's own default) and this is what fills in a real result later,
-- without this package needing to know how that result was produced.
-- name: SetImportUnitDecision :exec
UPDATE import_unit SET decision = sqlc.arg(decision), confidence = sqlc.arg(confidence),
                        work_id = sqlc.arg(work_id), edition_id = sqlc.arg(edition_id),
                        candidates_json = sqlc.arg(candidates_json), reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id);
