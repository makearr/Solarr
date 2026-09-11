-- ADR-50, todo 4.11 (migration 0026). Column lists follow table order.

-- name: CreatePack :one
INSERT INTO pack (kind, detected_from, confidence, title, acquisition_id, session_id,
                   member_count, created_at)
VALUES (sqlc.arg(kind), sqlc.arg(detected_from), sqlc.arg(confidence), sqlc.arg(title),
        sqlc.arg(acquisition_id), sqlc.arg(session_id), sqlc.arg(member_count), sqlc.arg(created_at))
RETURNING id, kind, detected_from, confidence, title, acquisition_id, session_id,
          member_count, resolved_count, created_at;

-- name: InsertPackItem :exec
INSERT INTO pack_item (pack_id, ord, work_id, edition_id, import_unit_id, state)
VALUES (sqlc.arg(pack_id), sqlc.arg(ord), sqlc.arg(work_id), sqlc.arg(edition_id),
        sqlc.arg(import_unit_id), sqlc.arg(state));

-- name: SetImportUnitPack :exec
UPDATE import_unit SET pack_id = ? WHERE id = ?;

-- name: GetPack :one
SELECT id, kind, detected_from, confidence, title, acquisition_id, session_id,
       member_count, resolved_count, created_at
FROM pack WHERE id = ?;

-- Cursor-paginated by id ascending, the same plain cursor /blocked and
-- import_unit's own page already use -- pack counts stay small today (one
-- discography import makes one row) but nothing bounds them the way a
-- session count is bounded (Invariant 22's own reasoning).
-- name: ListPacksPage :many
SELECT id, kind, detected_from, confidence, title, acquisition_id, session_id,
       member_count, resolved_count, created_at
FROM pack
WHERE id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: ListPackItems :many
SELECT pack_id, ord, work_id, edition_id, import_unit_id, state
FROM pack_item WHERE pack_id = ? ORDER BY ord;

-- Recomputed from pack_item rather than incremented as members resolve --
-- the same drift-and-repair shape RecomputeImportSessionCounts already uses.
-- name: RecomputePackCounts :exec
UPDATE pack SET
  member_count   = (SELECT COUNT(*) FROM pack_item pi WHERE pi.pack_id = sqlc.arg(pack_id)),
  resolved_count = (SELECT COUNT(*) FROM pack_item pi
                     WHERE pi.pack_id = sqlc.arg(pack_id) AND pi.state = 'RESOLVED')
WHERE id = sqlc.arg(pack_id);

-- A later placement (todo 4.9's ManualCommitter, an "assign a category
-- later" or "commit what was held" action reaching a unit that turned out
-- to belong to a pack) moves that one member from QUEUED to RESOLVED --
-- RecomputePackCounts is then what updates the pack's own resolved_count.
-- name: ResolvePackItem :exec
UPDATE pack_item SET state = 'RESOLVED', work_id = sqlc.arg(work_id), edition_id = sqlc.arg(edition_id)
WHERE import_unit_id = sqlc.arg(import_unit_id);
