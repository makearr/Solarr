-- Identified files under the given roots that were never asked about, or asked and not found before the cutoff.
-- name: ListLyricsCandidates :many
SELECT lp.media_object_id, lp.root_folder_id, lp.rel_path,
  w.canonical_title AS album_title,
  CAST(COALESCE((SELECT p.name FROM person p WHERE p.id = w.lead_person_id), '') AS TEXT) AS artist_name,
  CAST(COALESCE(wp.title, '') AS TEXT) AS track_title,
  CAST(COALESCE(mo.duration_ms, 0) AS INTEGER) AS duration_ms
FROM library_path lp
JOIN media_object mo ON mo.id = lp.media_object_id
JOIN work_file wf ON wf.media_object_id = lp.media_object_id
JOIN work w ON w.id = wf.work_id
LEFT JOIN work_part wp ON wp.id = (SELECT wp2.id FROM desired_item_file dif
    JOIN desired_item di ON di.id = dif.desired_item_id
    JOIN work_part wp2 ON wp2.work_id = di.work_id AND wp2.media_ref = dif.media_ref
    WHERE dif.media_object_id = lp.media_object_id LIMIT 1)
LEFT JOIN lyrics_attempt la ON la.media_object_id = lp.media_object_id
WHERE lp.media_object_id > sqlc.arg(after_id)
  AND lp.missing_since IS NULL
  AND lp.root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
  AND (la.media_object_id IS NULL OR (la.found = 0 AND la.attempted_at < sqlc.arg(retry_before)))
ORDER BY lp.media_object_id
LIMIT sqlc.arg(page_size);

-- name: RecordLyricsAttempt :exec
INSERT INTO lyrics_attempt (media_object_id, attempted_at, found)
VALUES (?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET attempted_at = excluded.attempted_at, found = excluded.found;
