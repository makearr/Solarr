-- What the statistics rollup measures (section 21.12): bounded aggregates, run by a background job and read back
-- from stat_rollup, never live.

-- Files and bytes per root folder and audio codec; a file with no measured codec yet is "unknown".
-- name: AggregateFilesByRootAndCodec :many
SELECT lp.root_folder_id,
       CAST(COALESCE(NULLIF(ma.audio_codec, ''), 'unknown') AS TEXT) AS codec,
       CAST(COUNT(*) AS INTEGER) AS files,
       CAST(COALESCE(SUM(mo.size_bytes), 0) AS INTEGER) AS bytes
FROM library_path lp
JOIN media_object mo ON mo.id = lp.media_object_id
LEFT JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
GROUP BY lp.root_folder_id, codec
ORDER BY lp.root_folder_id, codec;

-- Monitored albums per library and state.
-- name: AggregateAlbumsByCategoryAndState :many
SELECT category_id, state, CAST(COUNT(*) AS INTEGER) AS albums
FROM desired_item
WHERE satisfaction_kind = 'SET' AND monitored = 1
GROUP BY category_id, state
ORDER BY category_id, state;

-- Downloads since a time, per library and outcome.
-- name: AggregateAcquisitionsByCategoryAndState :many
SELECT d.category_id, a.state, CAST(COUNT(*) AS INTEGER) AS downloads
FROM acquisition a
JOIN desired_item d ON d.id = a.desired_item_id
WHERE a.created_at >= sqlc.arg(since)
GROUP BY d.category_id, a.state
ORDER BY d.category_id, a.state;

-- Files and bytes first seen per root folder and month: growth since Solarr began watching.
-- name: AggregateFilesAddedByRootAndMonth :many
SELECT lp.root_folder_id,
       CAST(strftime('%Y-%m', mo.first_seen_at, 'unixepoch') AS TEXT) AS month,
       CAST(COUNT(*) AS INTEGER) AS files,
       CAST(COALESCE(SUM(mo.size_bytes), 0) AS INTEGER) AS bytes
FROM library_path lp
JOIN media_object mo ON mo.id = lp.media_object_id
GROUP BY lp.root_folder_id, month
ORDER BY lp.root_folder_id, month;

-- Release decisions since a time, per library and outcome.
-- name: AggregateDecisionsByCategoryAndOutcome :many
SELECT d.category_id, cd.outcome, CAST(COUNT(*) AS INTEGER) AS decisions
FROM candidate_decision cd
JOIN desired_item d ON d.id = cd.desired_item_id
WHERE cd.decided_at >= sqlc.arg(since)
GROUP BY d.category_id, cd.outcome
ORDER BY d.category_id, cd.outcome;

-- name: DeleteStatRollupMetric :exec
DELETE FROM stat_rollup WHERE metric = ?;

-- The artists with something held in a library, and the release groups they hold there: what a discography is
-- compared against. A held work without a release-group id yields an empty id.
-- name: ListHeldReleaseGroupsByArtist :many
SELECT p.id AS person_id, x.external_id AS artist_mbid, p.name,
       CAST(COALESCE(g.external_id, '') AS TEXT) AS release_group_mbid
FROM person p
JOIN person_external_id x ON x.person_id = p.id AND x.provider = 'musicbrainz:artist'
JOIN credit c ON c.person_id = p.id AND c.role = 'artist' AND c.edition_id = 0 AND c.part_id = 0
JOIN desired_item di ON di.work_id = c.work_id AND di.category_id = sqlc.arg(category_id) AND di.satisfaction_kind = 'SET'
LEFT JOIN work_external_id g ON g.work_id = c.work_id AND g.provider = 'musicbrainz:release-group'
WHERE EXISTS (SELECT 1 FROM work_file wf WHERE wf.work_id = c.work_id)
GROUP BY p.id, x.external_id, g.external_id
ORDER BY p.id;

-- name: DeleteStatRollupMetricForCategory :exec
DELETE FROM stat_rollup WHERE metric = sqlc.arg(metric) AND dimension LIKE CAST(sqlc.arg(prefix) AS TEXT) || '%';

-- Files and bytes per root folder and measured video attributes; what the probe could not read is "unknown".
-- name: AggregateFilesByRootAndVideo :many
SELECT lp.root_folder_id,
       CAST(COALESCE(NULLIF(ma.video_codec, ''), 'unknown') AS TEXT) AS video_codec,
       CAST(COALESCE(NULLIF(ma.resolution, ''), 'unknown') AS TEXT) AS resolution,
       CAST(COALESCE(NULLIF(ma.hdr, ''), 'sdr') AS TEXT) AS hdr,
       CAST(COALESCE(NULLIF(ma.audio_codec, ''), 'unknown') AS TEXT) AS audio_codec,
       CAST(COALESCE(NULLIF(ma.audio_channels, ''), '') AS TEXT) AS audio_channels,
       CAST(COUNT(*) AS INTEGER) AS files,
       CAST(COALESCE(SUM(mo.size_bytes), 0) AS INTEGER) AS bytes
FROM library_path lp
JOIN media_object mo ON mo.id = lp.media_object_id
LEFT JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
WHERE ma.video_codec IS NOT NULL AND ma.video_codec <> ''
GROUP BY lp.root_folder_id, video_codec, resolution, hdr, audio_codec, audio_channels;

-- The 20 works of each library taking most space; each file counted once however many items cover it.
-- name: AggregateSeriesSize :many
SELECT category_id, work_id, title, files, bytes
FROM (SELECT x.category_id, x.work_id, w.canonical_title AS title,
             CAST(COUNT(*) AS INTEGER) AS files,
             CAST(COALESCE(SUM(mo.size_bytes), 0) AS INTEGER) AS bytes,
             ROW_NUMBER() OVER (PARTITION BY x.category_id ORDER BY COALESCE(SUM(mo.size_bytes), 0) DESC, x.work_id) AS place
      FROM (SELECT DISTINCT d.category_id, d.work_id, f.media_object_id
            FROM desired_item_file f JOIN desired_item d ON d.id = f.desired_item_id
            WHERE f.role = 'primary') x
      JOIN work w ON w.id = x.work_id
      JOIN media_object mo ON mo.id = x.media_object_id
      GROUP BY x.category_id, x.work_id)
WHERE place <= 20
ORDER BY category_id, bytes DESC, work_id;

-- Monitored episodes without a file, by series: aired ones are missing, the rest are still to come.
-- name: AggregateSeriesGaps :many
SELECT d.category_id, wp.work_id, w.canonical_title AS title,
       CAST(SUM(CASE WHEN COALESCE(wp.date, '') <> '' AND wp.date <= sqlc.arg(today) THEN 1 ELSE 0 END) AS INTEGER) AS missing,
       CAST(SUM(CASE WHEN COALESCE(wp.date, '') = '' OR wp.date > sqlc.arg(today) THEN 1 ELSE 0 END) AS INTEGER) AS unaired
FROM work_part wp
JOIN work w ON w.id = wp.work_id AND w.media_type = 'tv'
JOIN (SELECT DISTINCT work_id, category_id FROM desired_item) d ON d.work_id = wp.work_id
WHERE wp.monitored = 1 AND wp.media_ref NOT LIKE 's00e%'
  AND NOT EXISTS (SELECT 1 FROM desired_item_file f JOIN desired_item di ON di.id = f.desired_item_id
                  WHERE di.work_id = wp.work_id AND f.role = 'primary' AND f.media_ref = wp.media_ref)
GROUP BY d.category_id, wp.work_id
HAVING missing + unaired > 0
ORDER BY missing DESC, unaired DESC
LIMIT 2000;

-- Bytes per hour of picture and the average video bitrate by resolution and codec: bloated or starved files show here.
-- name: AggregateSizePerHour :many
SELECT lp.root_folder_id,
       CAST(ma.resolution AS TEXT) AS resolution,
       CAST(ma.video_codec AS TEXT) AS video_codec,
       CAST(COUNT(*) AS INTEGER) AS files,
       CAST(SUM(mo.size_bytes) AS INTEGER) AS bytes,
       CAST(SUM(mo.duration_ms) AS INTEGER) AS duration_ms,
       CAST(COALESCE(AVG(NULLIF(mo.video_bitrate, 0)), 0) AS INTEGER) AS video_bitrate
FROM library_path lp
JOIN media_object mo ON mo.id = lp.media_object_id
JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
WHERE ma.video_codec IS NOT NULL AND ma.video_codec <> '' AND ma.resolution IS NOT NULL AND mo.duration_ms > 0
GROUP BY lp.root_folder_id, resolution, video_codec;
