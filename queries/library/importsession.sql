-- ADR-48 (migration 0018). Column lists follow table order, or sqlc stops
-- reusing models.

-- name: CreateImportSession :one
INSERT INTO import_session (source_path, origin, acquisition_id, category_id, state,
                             threshold, started_at, preview)
VALUES (sqlc.arg(source_path), sqlc.arg(origin), sqlc.arg(acquisition_id),
        sqlc.arg(category_id), 'INSPECTING', sqlc.arg(threshold), sqlc.arg(started_at), sqlc.arg(preview))
RETURNING id, source_path, origin, acquisition_id, category_id, state, threshold,
          units_total, units_committed, units_queued, bytes_imported, extract_dir,
          started_at, finished_at, error, units_skipped, units_failed, units_estimated, dismissed_at, preview;

-- name: GetImportSession :one
SELECT id, source_path, origin, acquisition_id, category_id, state, threshold,
       units_total, units_committed, units_queued, bytes_imported, extract_dir,
       started_at, finished_at, error, units_skipped, units_failed, units_estimated, dismissed_at, preview
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
       started_at, finished_at, error, units_skipped, units_failed, units_estimated, dismissed_at, preview
FROM import_session
WHERE (sqlc.arg(filter_by_state) = 0 OR state = sqlc.arg(state))
  AND (sqlc.arg(include_dismissed) = 1 OR dismissed_at IS NULL)
ORDER BY started_at DESC;

-- Hides a finished session from the Import screen without deleting anything:
-- the row is still what a revert reads. Only from a state that has stopped,
-- so a run in progress cannot be dismissed out from under itself. :execrows
-- distinguishes "no such session, or still running" from "now hidden".
-- name: DismissImportSession :execrows
UPDATE import_session SET dismissed_at = sqlc.arg(dismissed_at)
WHERE id = sqlc.arg(id) AND dismissed_at IS NULL
  AND state IN ('DONE', 'FAILED', 'CANCELLED', 'REVERTED');

-- name: SetImportSessionExtractDir :exec
UPDATE import_session SET extract_dir = ? WHERE id = ?;

-- From no category, or a no-op onto the same one -- the second form is what
-- makes re-running the placement catch-up idempotent (a caller retrying
-- after a transient placement failure, e.g. capacity not probed yet, sends
-- the same category_id again). Re-pointing at a *different* category is out
-- of scope, still refused. :execrows lets the caller tell that apart from
-- "no such session".
-- name: SetImportSessionCategory :execrows
UPDATE import_session SET category_id = sqlc.arg(category_id)
WHERE id = sqlc.arg(id) AND (category_id IS NULL OR category_id = sqlc.arg(category_id));

-- Set once, the first time a session's estimate is computed (todo 4.21's ETA);
-- never recomputed on a later resume of the same session.
-- name: SetImportSessionUnitsEstimated :exec
UPDATE import_session SET units_estimated = ? WHERE id = ?;

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

-- Units a pass recorded but could not finish, so a resumed walk inspects them again.
-- name: DeleteImportUnits :exec
DELETE FROM import_unit WHERE session_id = sqlc.arg(session_id) AND id IN (sqlc.slice('ids'));

-- name: InsertImportUnit :one
INSERT INTO import_unit (session_id, rel_path, shape, file_count, bytes, confidence,
                          decision, work_id, edition_id, evidence_json, candidates_json,
                          reason)
VALUES (sqlc.arg(session_id), sqlc.arg(rel_path), sqlc.arg(shape), sqlc.arg(file_count),
        sqlc.arg(bytes), sqlc.arg(confidence), sqlc.arg(decision), sqlc.arg(work_id),
        sqlc.arg(edition_id), sqlc.arg(evidence_json), sqlc.arg(candidates_json),
        sqlc.arg(reason))
RETURNING id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
          work_id, edition_id, evidence_json, candidates_json, reason, work_created,
          edition_created, pack_id;

-- name: ListImportUnits :many
SELECT id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
       work_id, edition_id, evidence_json, candidates_json, reason, work_created,
       edition_created, pack_id
FROM import_unit WHERE session_id = ? ORDER BY id;

-- Cursor-paginated, unlike ListImportUnits above: a session's own unit count
-- is not bounded the way a session count is (Invariant 22 is exactly the
-- "500k+ files" case a whole-library adoption run can hit), so the API layer
-- needs a real page, not the whole session's units in one response.
-- ListImportUnits itself stays as-is for the job's own small, in-process needs
-- (resumeOrCreate's "the last unit committed so far" lookup).
-- name: ListImportUnitsPage :many
SELECT id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
       work_id, edition_id, evidence_json, candidates_json, reason, work_created,
       edition_created, pack_id
FROM import_unit
WHERE session_id = sqlc.arg(session_id) AND id > sqlc.arg(after_id)
  AND (sqlc.arg(filter_by_decision) = 0 OR decision = sqlc.arg(decision))
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: GetImportUnit :one
SELECT id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
       work_id, edition_id, evidence_json, candidates_json, reason, work_created,
       edition_created, pack_id
FROM import_unit WHERE id = ?;

-- The only mutation IDENTIFY needs once it exists: a unit starts QUEUE with no
-- identity (F29's own default) and this is what fills in a real result later,
-- without this package needing to know how that result was produced.
-- name: SetImportUnitDecision :exec
UPDATE import_unit SET decision = sqlc.arg(decision), confidence = sqlc.arg(confidence),
                        work_id = sqlc.arg(work_id), edition_id = sqlc.arg(edition_id),
                        candidates_json = sqlc.arg(candidates_json), reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id);

-- Every unit this session decided COMMIT but has not yet actually placed --
-- decision alone means "confidence cleared the threshold", not "in the
-- library" (job.go's own doc comment); work_id staying NULL after DECIDE is
-- what distinguishes the two, since nothing sets it before the IMPORTING
-- phase runs.
-- name: ListUnplacedCommitUnits :many
SELECT id, session_id, rel_path, shape, file_count, bytes, confidence, decision,
       work_id, edition_id, evidence_json, candidates_json, reason, work_created,
       edition_created, pack_id
FROM import_unit
WHERE session_id = ? AND decision = 'COMMIT' AND work_id IS NULL
ORDER BY id;

-- The IMPORTING phase's own write, once placement.Resolve, the move and
-- ResolveWorksDetailed have all succeeded for one unit -- work_id becoming
-- non-null is what "actually placed" means from here on.
-- name: SetImportUnitPlacement :exec
UPDATE import_unit SET work_id = sqlc.arg(work_id), edition_id = sqlc.arg(edition_id),
                        work_created = sqlc.arg(work_created),
                        edition_created = sqlc.arg(edition_created)
WHERE id = sqlc.arg(id);

-- library_path_id is nullable: 0029's own migration comment explains why
-- (which path this unit's own placement created, when it can be known).
-- name: InsertImportUnitFile :exec
INSERT INTO import_unit_file (unit_id, media_object_id, library_path_id, transfer_method)
VALUES (?, ?, ?, ?);

-- name: ListImportUnitFilesForSession :many
SELECT iuf.unit_id, iuf.media_object_id, iuf.library_path_id, iuf.transfer_method
FROM import_unit_file iuf
JOIN import_unit iu ON iu.id = iuf.unit_id
WHERE iu.session_id = ?
ORDER BY iuf.id;

-- Import revert's own first step for one unit (todo 4.9): work_id/edition_id
-- going back to NULL is what makes it safe to then delete the work/edition
-- row itself (import_unit's own FK to either carries no ON DELETE CASCADE,
-- and every unit that ever referenced a work -- created or only matched --
-- must be cleared before that work can be deleted, not only the unit that
-- created it).
-- name: ClearImportUnitPlacement :exec
UPDATE import_unit SET work_id = NULL, edition_id = NULL, work_created = 0, edition_created = 0
WHERE id = ?;

-- One row per (unit, method) for a whole session, so the import screen can
-- say how each unit's files actually arrived without reading every file row.
-- Mirrors ListImportUnitFilesForSession's own join and filter; rows written
-- before migration 0034 carry no method and are left out rather than guessed.
-- name: CountImportUnitTransferMethodsForSession :many
SELECT iuf.unit_id, iuf.transfer_method, CAST(COUNT(*) AS INTEGER) AS file_count
FROM import_unit_file iuf
JOIN import_unit iu ON iu.id = iuf.unit_id
WHERE iu.session_id = ? AND iuf.transfer_method IS NOT NULL
GROUP BY iuf.unit_id, iuf.transfer_method
ORDER BY iuf.unit_id, iuf.transfer_method;

-- A download folder imports each entry once: any session, finished or dismissed, counts.
-- name: CountImportSessionsForSource :one
SELECT CAST(COUNT(*) AS INTEGER) FROM import_session WHERE source_path = ?;

-- name: InsertImportUnitExtra :exec
INSERT INTO import_unit_extra (unit_id, root_folder_id, rel_path) VALUES (?, ?, ?);

-- name: ListImportUnitExtrasForSession :many
SELECT iue.id, iue.unit_id, iue.root_folder_id, iue.rel_path
FROM import_unit_extra iue
JOIN import_unit iu ON iu.id = iue.unit_id
WHERE iu.session_id = ?
ORDER BY iue.id;

-- name: DeleteImportUnitExtras :exec
DELETE FROM import_unit_extra WHERE unit_id = ?;

-- The newest import a download's poll started, to follow it to IMPORTED.
-- name: GetLatestImportSessionForAcquisition :one
SELECT id, source_path, origin, acquisition_id, category_id, state, threshold,
       units_total, units_committed, units_queued, bytes_imported, extract_dir,
       started_at, finished_at, error, units_skipped, units_failed, units_estimated, dismissed_at, preview
FROM import_session WHERE acquisition_id = ? ORDER BY id DESC LIMIT 1;
