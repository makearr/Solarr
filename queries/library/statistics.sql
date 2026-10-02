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
