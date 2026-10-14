-- One work folded into another of its type: everything that pointed at the dropped work points at the kept one.
-- Each statement moves one kind of row; Identity.MergeWork runs them in one transaction.

-- name: CountWorkMerge :one
SELECT
  CAST((SELECT COUNT(*) FROM work_file wf WHERE wf.work_id = sqlc.arg(drop_id)) AS INTEGER) AS files,
  CAST((SELECT COUNT(*) FROM desired_item d WHERE d.work_id = sqlc.arg(drop_id)) AS INTEGER) AS wanted_items,
  CAST((SELECT COUNT(*) FROM acquisition a JOIN desired_item d ON d.id = a.desired_item_id
        WHERE d.work_id = sqlc.arg(drop_id)) AS INTEGER) AS acquisitions,
  CAST((SELECT COUNT(*) FROM import_unit u WHERE u.work_id = sqlc.arg(drop_id)) AS INTEGER) AS import_units;

-- name: MergeWorkFiles :exec
INSERT OR IGNORE INTO work_file (work_id, media_object_id, first_seen_at)
SELECT sqlc.arg(keep_id), wf.media_object_id, wf.first_seen_at FROM work_file wf WHERE wf.work_id = sqlc.arg(drop_id);

-- name: MergeWorkRootFolders :exec
INSERT OR IGNORE INTO work_root_folder (work_id, root_folder_id, first_seen_at)
SELECT sqlc.arg(keep_id), r.root_folder_id, r.first_seen_at FROM work_root_folder r WHERE r.work_id = sqlc.arg(drop_id);

-- A file keeps covering a part only where the kept work has that part; the media type places the rest again.
-- name: MergeDesiredItemFiles :exec
INSERT OR IGNORE INTO desired_item_file (desired_item_id, media_object_id, role, media_ref, added_at)
SELECT sqlc.arg(to_item_id), f.media_object_id, f.role, f.media_ref, f.added_at
FROM desired_item_file f
WHERE f.desired_item_id = sqlc.arg(from_item_id)
  AND (f.media_ref = '' OR EXISTS (SELECT 1 FROM work_part p WHERE p.work_id = sqlc.arg(keep_id) AND p.media_ref = f.media_ref));

-- name: MoveAcquisitionsToItem :exec
UPDATE acquisition SET desired_item_id = sqlc.arg(to_item_id) WHERE desired_item_id = sqlc.arg(from_item_id);

-- An item the kept work has no counterpart for moves over whole, without the dropped work's edition.
-- name: MoveDesiredItemToWork :exec
UPDATE desired_item SET work_id = sqlc.arg(keep_id), edition_id = NULL, dirty = 1 WHERE id = sqlc.arg(item_id);

-- name: DropUnplacedItemFiles :exec
DELETE FROM desired_item_file
WHERE desired_item_id = sqlc.arg(item_id) AND media_ref <> ''
  AND NOT EXISTS (SELECT 1 FROM work_part p WHERE p.work_id = sqlc.arg(keep_id) AND p.media_ref = desired_item_file.media_ref);

-- name: MergeWorkImportUnits :exec
UPDATE import_unit SET work_id = sqlc.arg(keep_id),
  edition_id = CASE WHEN edition_id IN (SELECT e.id FROM edition e WHERE e.work_id = sqlc.arg(drop_id)) THEN NULL ELSE edition_id END
WHERE work_id = sqlc.arg(drop_id) OR edition_id IN (SELECT e.id FROM edition e WHERE e.work_id = sqlc.arg(drop_id));

-- name: MergeWorkPackItems :exec
UPDATE pack_item SET work_id = sqlc.arg(keep_id),
  edition_id = CASE WHEN edition_id IN (SELECT e.id FROM edition e WHERE e.work_id = sqlc.arg(drop_id)) THEN NULL ELSE edition_id END
WHERE work_id = sqlc.arg(drop_id) OR edition_id IN (SELECT e.id FROM edition e WHERE e.work_id = sqlc.arg(drop_id));

-- name: MergeWorkArrImportItems :exec
UPDATE arr_import_item SET work_id = sqlc.arg(keep_id) WHERE work_id = sqlc.arg(drop_id);

-- name: MergeWorkRequests :exec
UPDATE request SET work_id = sqlc.arg(keep_id) WHERE work_id = sqlc.arg(drop_id);

-- name: MergeWorkManualIdentifications :exec
UPDATE manual_identification SET work_id = sqlc.arg(keep_id) WHERE work_id = sqlc.arg(drop_id);

-- name: MergeWorkTags :exec
INSERT OR IGNORE INTO tag_assignment (tag_id, subject_type, subject_id, source)
SELECT a.tag_id, a.subject_type, sqlc.arg(keep_id), a.source FROM tag_assignment a
WHERE a.subject_type = 'work' AND a.subject_id = sqlc.arg(drop_id);

-- name: DeleteWorkTags :exec
DELETE FROM tag_assignment WHERE subject_type = 'work' AND subject_id = ?;

-- The dropped work's ids keep resolving, to the kept work, as aliases.
-- name: MergeWorkExternalIDs :exec
UPDATE work_external_id SET work_id = sqlc.arg(keep_id), confidence = MIN(confidence, CAST(sqlc.arg(confidence) AS REAL))
WHERE work_id = sqlc.arg(drop_id);

-- name: FindEditionByLabel :one
SELECT id FROM edition WHERE work_id = sqlc.arg(work_id) AND kind = sqlc.arg(kind) AND label = sqlc.arg(label)
ORDER BY id LIMIT 1;

-- name: MoveEditionToWork :exec
UPDATE edition SET work_id = sqlc.arg(keep_id), is_default = 0 WHERE id = sqlc.arg(edition_id);

-- name: SuggestWorkMerge :exec
INSERT INTO work_merge_suggestion (work_id, into_work_id, found_at) VALUES (?, ?, ?)
ON CONFLICT (work_id) DO UPDATE SET into_work_id = excluded.into_work_id, found_at = excluded.found_at;

-- name: GetWorkMergeSuggestion :one
SELECT s.into_work_id, w.canonical_title, CAST(COALESCE(p.name, '') AS TEXT) AS lead_name
FROM work_merge_suggestion s
JOIN work w ON w.id = s.into_work_id
LEFT JOIN person p ON p.id = w.lead_person_id
WHERE s.work_id = ?;

-- name: DeleteWorkMergeSuggestion :exec
DELETE FROM work_merge_suggestion WHERE work_id = ?;
