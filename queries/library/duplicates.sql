-- Every file that covers a track, with what identifies the track: its title, artist and length. Duplicates are found
-- from these in Go, by what the file is and not by its bytes.
-- name: ListTrackFilesWithMetadata :many
SELECT mo.id AS media_object_id, mo.size_bytes,
       CAST(COALESCE(mo.duration_ms, 0) AS INTEGER) AS duration_ms,
       CAST(COALESCE(mo.overall_bitrate, mo.audio_bitrate, 0) AS INTEGER) AS bitrate,
       CAST(COALESCE(a.audio_codec, '') AS TEXT) AS audio_codec,
       CAST(COALESCE(a.container, '') AS TEXT) AS container,
       CAST(COALESCE(a.lossless, 0) AS INTEGER) AS lossless,
       CAST(COALESCE(a.bit_depth, 0) AS INTEGER) AS bit_depth,
       CAST(COALESCE(a.sample_rate, 0) AS INTEGER) AS sample_rate,
       CAST(COALESCE(wp.title, '') AS TEXT) AS track_title,
       w.canonical_title AS album_title,
       d.work_id, wp.id AS part_id, w.media_type, CAST(COALESCE(d.edition_id, 0) AS INTEGER) AS edition_id,
       CAST(COALESCE((SELECT tp.name FROM credit tc JOIN person tp ON tp.id = tc.person_id
                      WHERE tc.work_id = d.work_id AND tc.part_id = wp.id AND tc.role = 'artist' ORDER BY tc.ord LIMIT 1),
                     (SELECT ap.name FROM person ap WHERE ap.id = w.lead_person_id), '') AS TEXT) AS artist,
       lp.root_folder_id, lp.rel_path, mo.audio_fp
FROM desired_item_file f
JOIN desired_item d ON d.id = f.desired_item_id
JOIN work w ON w.id = d.work_id
JOIN work_part wp ON wp.work_id = d.work_id AND wp.media_ref = f.media_ref
JOIN media_object mo ON mo.id = f.media_object_id
LEFT JOIN media_attributes a ON a.id = mo.measured_attributes_id
JOIN library_path lp ON lp.media_object_id = mo.id
WHERE f.role = 'primary' AND f.media_ref <> '' AND d.category_id = sqlc.arg(category_id)
ORDER BY mo.id, lp.is_primary DESC, lp.id;

-- Files of a library's works that cover no track (a second copy of an album's file, placed beside it): they cannot be
-- told by a title, only by their audio fingerprint, so those with none are left out.
-- name: ListUnattachedAudioFiles :many
SELECT mo.id AS media_object_id, mo.size_bytes,
       CAST(COALESCE(mo.duration_ms, 0) AS INTEGER) AS duration_ms,
       CAST(COALESCE(mo.overall_bitrate, mo.audio_bitrate, 0) AS INTEGER) AS bitrate,
       CAST(COALESCE(a.audio_codec, '') AS TEXT) AS audio_codec,
       CAST(COALESCE(a.container, '') AS TEXT) AS container,
       CAST(COALESCE(a.lossless, 0) AS INTEGER) AS lossless,
       CAST(COALESCE(a.bit_depth, 0) AS INTEGER) AS bit_depth,
       CAST(COALESCE(a.sample_rate, 0) AS INTEGER) AS sample_rate,
       w.canonical_title AS album_title, wf.work_id,
       lp.root_folder_id, lp.rel_path, mo.audio_fp
FROM work_file wf
JOIN work w ON w.id = wf.work_id AND w.media_type = 'music'
JOIN media_object mo ON mo.id = wf.media_object_id
LEFT JOIN media_attributes a ON a.id = mo.measured_attributes_id
JOIN library_path lp ON lp.media_object_id = mo.id AND lp.missing_since IS NULL
WHERE mo.audio_fp IS NOT NULL AND length(mo.audio_fp) > 0
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = wf.work_id AND d.category_id = sqlc.arg(category_id))
  AND NOT EXISTS (SELECT 1 FROM desired_item_file f WHERE f.media_object_id = mo.id AND f.role = 'primary' AND f.media_ref <> '')
ORDER BY mo.id, lp.is_primary DESC, lp.id;
