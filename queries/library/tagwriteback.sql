-- Files whose work and edition carry the given providers' ids and that were never written,
-- with each id the tags could take. Providers are arguments: the query knows no media type.
-- name: ListTagWritebackCandidates :many
SELECT lp.media_object_id, lp.root_folder_id, lp.rel_path, wf.work_id,
  CAST(COALESCE((SELECT we.external_id FROM work_external_id we
    WHERE we.work_id = wf.work_id AND we.provider = sqlc.arg(work_provider)
    ORDER BY we.confidence DESC LIMIT 1), '') AS TEXT) AS work_external_id,
  CAST(COALESCE((SELECT ee.external_id FROM edition e
    JOIN edition_external_id ee ON ee.edition_id = e.id AND ee.provider = sqlc.arg(edition_provider)
    WHERE e.work_id = wf.work_id ORDER BY e.is_default DESC, e.id LIMIT 1), '') AS TEXT) AS edition_external_id,
  CAST(COALESCE((SELECT pe.external_id FROM desired_item_file dif
    JOIN desired_item di ON di.id = dif.desired_item_id
    JOIN work_part wp ON wp.work_id = di.work_id AND wp.media_ref = dif.media_ref
    JOIN work_part_external_id pe ON pe.part_id = wp.id AND pe.provider = sqlc.arg(part_provider)
    WHERE dif.media_object_id = lp.media_object_id LIMIT 1), '') AS TEXT) AS part_external_id,
  CAST(COALESCE((SELECT px.external_id FROM work w
    JOIN person_external_id px ON px.person_id = w.primary_artist_id AND px.provider = sqlc.arg(person_provider)
    WHERE w.id = wf.work_id LIMIT 1), '') AS TEXT) AS person_external_id
FROM library_path lp
JOIN work_file wf ON wf.media_object_id = lp.media_object_id
WHERE lp.media_object_id > sqlc.arg(after_id)
  AND lp.missing_since IS NULL
  AND lp.root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
  AND NOT EXISTS (SELECT 1 FROM tag_writeback tw WHERE tw.media_object_id = lp.media_object_id)
  AND EXISTS (SELECT 1 FROM work_external_id we
    WHERE we.work_id = wf.work_id AND we.provider = sqlc.arg(work_provider))
  AND EXISTS (SELECT 1 FROM edition e
    JOIN edition_external_id ee ON ee.edition_id = e.id AND ee.provider = sqlc.arg(edition_provider)
    WHERE e.work_id = wf.work_id)
ORDER BY lp.media_object_id
LIMIT sqlc.arg(page_size);

-- name: RecordTagWriteback :exec
INSERT INTO tag_writeback (media_object_id, attempted_at, written, error)
VALUES (?, ?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET
  attempted_at = excluded.attempted_at, written = excluded.written, error = excluded.error;
