-- name: UpsertFileDefect :exec
INSERT INTO file_defect (media_object_id, reason, detail, size_bytes, detected_at)
VALUES (sqlc.arg(media_object_id), sqlc.arg(reason), sqlc.arg(detail), sqlc.arg(size_bytes), sqlc.arg(detected_at))
ON CONFLICT (media_object_id) DO UPDATE SET reason = excluded.reason, detail = excluded.detail, size_bytes = excluded.size_bytes, detected_at = excluded.detected_at;

-- name: DeleteFileDefect :exec
DELETE FROM file_defect WHERE media_object_id = ?;

-- The defective files on the given roots, the primary path of each and the work it belongs to; keyset on the media object id.
-- name: ListFileDefects :many
SELECT d.media_object_id, d.reason, d.detail, d.size_bytes, d.detected_at, lp.root_folder_id, lp.rel_path,
       CAST(COALESCE((SELECT wf.work_id FROM work_file wf WHERE wf.media_object_id = d.media_object_id ORDER BY wf.work_id LIMIT 1), 0) AS INTEGER) AS work_id,
       CAST(COALESCE((SELECT w.canonical_title FROM work_file wf JOIN work w ON w.id = wf.work_id WHERE wf.media_object_id = d.media_object_id ORDER BY wf.work_id LIMIT 1), '') AS TEXT) AS work_title
FROM file_defect d
JOIN library_path lp ON lp.media_object_id = d.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE lp.root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids))) AND d.media_object_id > sqlc.arg(after_id)
ORDER BY d.media_object_id
LIMIT sqlc.arg(page_size);

-- name: CountFileDefects :one
SELECT CAST(COUNT(*) AS INTEGER) FROM file_defect d
JOIN library_path lp ON lp.media_object_id = d.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE lp.root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)));

-- The roots of the given files, so a bulk action can be checked against the library it was asked in.
-- name: ListFileDefectRoots :many
SELECT d.media_object_id, lp.root_folder_id FROM file_defect d
JOIN library_path lp ON lp.media_object_id = d.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE d.media_object_id IN (SELECT value FROM json_each(sqlc.arg(ids)));
