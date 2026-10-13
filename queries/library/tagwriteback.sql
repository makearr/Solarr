-- Identified files under the given roots with everything their tags could take and the digest
-- of what was last written. Providers are arguments: the query knows no media type.
-- name: ListTagWritebackCandidates :many
SELECT lp.media_object_id, lp.root_folder_id, lp.rel_path, wf.work_id,
  w.canonical_title AS work_title,
  CAST(COALESCE((SELECT e.year FROM edition e WHERE e.work_id = wf.work_id AND e.year IS NOT NULL
    ORDER BY e.is_default DESC, e.id LIMIT 1), w.year, 0) AS INTEGER) AS year,
  CAST(COALESCE((SELECT p.name FROM person p WHERE p.id = w.lead_person_id), '') AS TEXT) AS artist_name,
  CAST(COALESCE(wp.media_ref, '') AS TEXT) AS media_ref,
  CAST(COALESCE(wp.title, '') AS TEXT) AS part_title,
  CAST(COALESCE((SELECT GROUP_CONCAT(name, '; ') FROM (SELECT t.name FROM tag_assignment ta
    JOIN tag t ON t.id = ta.tag_id
    WHERE ta.subject_type = 'work' AND ta.subject_id = wf.work_id AND ta.source = sqlc.arg(genre_source)
    ORDER BY t.name)), '') AS TEXT) AS genres,
  CAST(COALESCE((SELECT a.cache_key FROM artwork a
    WHERE a.subject_type = 'work' AND a.subject_id = wf.work_id AND a.kind = 'cover'), '') AS TEXT) AS cover_key,
  CAST(COALESCE((SELECT a.fetched_at FROM artwork a
    WHERE a.subject_type = 'work' AND a.subject_id = wf.work_id AND a.kind = 'cover'), 0) AS INTEGER) AS cover_fetched_at,
  CAST(COALESCE((SELECT we.external_id FROM work_external_id we
    WHERE we.work_id = wf.work_id AND we.provider = sqlc.arg(work_provider)
    ORDER BY we.confidence DESC LIMIT 1), '') AS TEXT) AS work_external_id,
  CAST(COALESCE((SELECT ee.external_id FROM edition e
    JOIN edition_external_id ee ON ee.edition_id = e.id AND ee.provider = sqlc.arg(edition_provider)
    WHERE e.work_id = wf.work_id ORDER BY e.is_default DESC, e.id, ee.confidence DESC LIMIT 1), '') AS TEXT) AS edition_external_id,
  CAST(COALESCE((SELECT pe.external_id FROM work_part_external_id pe
    WHERE pe.part_id = wp.id AND pe.provider = sqlc.arg(part_provider) LIMIT 1), '') AS TEXT) AS part_external_id,
  CAST(COALESCE((SELECT px.external_id FROM person_external_id px
    WHERE px.person_id = w.lead_person_id AND px.provider = sqlc.arg(person_provider) LIMIT 1), '') AS TEXT) AS person_external_id,
  CAST(COALESCE(tw.digest, '') AS TEXT) AS digest
FROM library_path lp
JOIN work_file wf ON wf.media_object_id = lp.media_object_id
JOIN work w ON w.id = wf.work_id
LEFT JOIN work_part wp ON wp.id = (SELECT wp2.id FROM desired_item_file dif
    JOIN desired_item di ON di.id = dif.desired_item_id
    JOIN work_part wp2 ON wp2.work_id = di.work_id AND wp2.media_ref = dif.media_ref
    WHERE dif.media_object_id = lp.media_object_id LIMIT 1)
LEFT JOIN tag_writeback tw ON tw.media_object_id = lp.media_object_id
WHERE lp.media_object_id > sqlc.arg(after_id)
  AND lp.missing_since IS NULL
  AND lp.root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
  AND EXISTS (SELECT 1 FROM work_external_id we
    WHERE we.work_id = wf.work_id AND we.provider = sqlc.arg(work_provider))
  AND EXISTS (SELECT 1 FROM edition e
    JOIN edition_external_id ee ON ee.edition_id = e.id AND ee.provider = sqlc.arg(edition_provider)
    WHERE e.work_id = wf.work_id)
ORDER BY lp.media_object_id
LIMIT sqlc.arg(page_size);

-- name: RecordTagWriteback :exec
INSERT INTO tag_writeback (media_object_id, attempted_at, written, error, digest)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET
  attempted_at = excluded.attempted_at, written = excluded.written, error = excluded.error,
  digest = excluded.digest;
