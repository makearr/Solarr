-- Collections, the ready queue, artwork, statistics and reputation.

-- name: CreateCollection :one
INSERT INTO collection (media_type, name, kind, source, monitored, complete_at, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, media_type, name, kind, source, monitored, complete_at, added_at;

-- name: AddCollectionMember :exec
INSERT INTO collection_member (collection_id, work_id, ord)
VALUES (?, ?, ?)
ON CONFLICT (collection_id, work_id) DO UPDATE SET ord = excluded.ord;

-- name: ListCollectionMembers :many
SELECT collection_id, work_id, ord
FROM collection_member WHERE collection_id = ? ORDER BY ord, work_id;

-- "You have 4 of 6 Ihaol films" is the first thing library-derived discovery
-- looks for, so held and stated counts come back together (ADR-38).
-- name: CollectionCoverage :one
SELECT c.id, c.name, c.complete_at,
       CAST(COUNT(DISTINCT m.work_id) AS INTEGER) AS members,
       CAST(COUNT(DISTINCT CASE WHEN d.state = 'COMPLETE' THEN m.work_id END) AS INTEGER) AS held
FROM collection c
LEFT JOIN collection_member m ON m.collection_id = c.id
LEFT JOIN desired_item d ON d.work_id = m.work_id
WHERE c.id = ?
GROUP BY c.id;

-- name: ListCollectionsForWork :many
SELECT c.id, c.media_type, c.name, c.kind, c.source, c.monitored, c.complete_at, c.added_at
FROM collection c
JOIN collection_member m ON m.collection_id = c.id
WHERE m.work_id = ? ORDER BY c.name;

-- Primed so something dispatches the instant a slot frees (section 10.5).
-- name: EnqueueReady :exec
INSERT INTO ready_queue (candidate_id, desired_item_id, priority, queued_at,
                         expires_at, size_bytes)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (candidate_id) DO UPDATE SET
  priority = excluded.priority,
  expires_at = excluded.expires_at;

-- Revalidated at dispatch, so an expired entry is never handed to a downloader.
-- name: NextReady :many
SELECT candidate_id, desired_item_id, priority, queued_at, expires_at, size_bytes
FROM ready_queue
WHERE expires_at > sqlc.arg(now)
ORDER BY priority DESC, queued_at ASC
LIMIT sqlc.arg(page_size);

-- name: DequeueReady :exec
DELETE FROM ready_queue WHERE candidate_id = ?;

-- name: PurgeExpiredReady :exec
DELETE FROM ready_queue WHERE expires_at <= ?;

-- name: UpsertArtwork :exec
INSERT INTO artwork (subject_type, subject_id, kind, source_url, cache_key,
                     width, height, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (subject_type, subject_id, kind) DO UPDATE SET
  source_url = excluded.source_url,
  cache_key = excluded.cache_key,
  width = excluded.width,
  height = excluded.height,
  fetched_at = excluded.fetched_at;

-- name: ListArtworkForSubject :many
SELECT id, subject_type, subject_id, kind, source_url, cache_key, width, height, fetched_at
FROM artwork WHERE subject_type = ? AND subject_id = ? ORDER BY kind;

-- name: GetArtwork :one
SELECT cache_key FROM artwork WHERE subject_type = ? AND subject_id = ? AND kind = ?;

-- Cover art attaches to the work (subject_type='work'), but Cover Art Archive is
-- queried by release (edition) MBID -- any refined (kind != 'unknown') edition
-- with one is a usable source. is_default is never set to 1 anywhere in this
-- codebase today (found writing this query), so the lowest edition id per work
-- is the deterministic tie-break instead, not a real "preferred edition" signal.
-- 'musicbrainz:release' must match internal/media/music.ProviderRelease -- the
-- release-group id (internal/media/music.ProviderReleaseGroup) identifies the work,
-- not the edition, and is the wrong id for this API (section 19.6's tag table).
-- name: ListWorksNeedingArtwork :many
SELECT e.work_id AS work_id, x.external_id AS release_mbid
FROM edition e
JOIN edition_external_id x ON x.edition_id = e.id AND x.provider = 'musicbrainz:release'
WHERE e.kind != 'unknown'
  AND e.id = (
    SELECT MIN(e2.id) FROM edition e2
    JOIN edition_external_id x2 ON x2.edition_id = e2.id AND x2.provider = 'musicbrainz:release'
    WHERE e2.work_id = e.work_id AND e2.kind != 'unknown'
  )
  AND NOT EXISTS (
    SELECT 1 FROM artwork a WHERE a.subject_type = 'work' AND a.subject_id = e.work_id AND a.kind = 'cover'
  )
ORDER BY e.work_id
LIMIT sqlc.arg(page_size);

-- name: SetStatRollup :exec
INSERT INTO stat_rollup (metric, dimension, period, value, count, computed_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (metric, dimension, period) DO UPDATE SET
  value = excluded.value,
  count = excluded.count,
  computed_at = excluded.computed_at;

-- Every chart reads a rollup, never a live aggregate (ADR-28, section 21.12).
-- name: ListStatRollup :many
SELECT metric, dimension, period, value, count, computed_at
FROM stat_rollup
WHERE metric = sqlc.arg(metric) AND period >= sqlc.arg(since)
ORDER BY period DESC, dimension
LIMIT sqlc.arg(page_size);

-- Only content-attributable outcomes count: a full disk is not the group's
-- fault, and counting it would make reputation measure the environment (ADR-45).
-- name: RecordGroupObservation :exec
INSERT INTO group_observation (release_group, media_object_id, outcome, cause,
                               observed_at, was_exploration)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListGroupObservations :many
SELECT id, release_group, media_object_id, outcome, cause, observed_at, was_exploration
FROM group_observation
WHERE release_group = ? AND observed_at >= ? ORDER BY observed_at DESC;

-- Recomputable from the observations, which is why they are kept individually.
-- name: CountGroupOutcomes :many
SELECT outcome, CAST(COUNT(*) AS INTEGER) AS total
FROM group_observation
WHERE release_group = sqlc.arg(release_group)
  AND cause = 'content'
  AND observed_at >= sqlc.arg(since)
GROUP BY outcome;

-- name: SetGroupReputation :exec
INSERT INTO group_reputation (release_group, samples, raw_success, bounded_success,
                              corruption_rate, mislabel_rate, last_observed_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (release_group) DO UPDATE SET
  samples = excluded.samples,
  raw_success = excluded.raw_success,
  bounded_success = excluded.bounded_success,
  corruption_rate = excluded.corruption_rate,
  mislabel_rate = excluded.mislabel_rate,
  last_observed_at = excluded.last_observed_at,
  updated_at = excluded.updated_at;

-- Scoring reads the bounded value, never the raw rate (ADR-45).
-- name: GetGroupReputation :one
SELECT release_group, samples, raw_success, bounded_success, corruption_rate,
       mislabel_rate, last_observed_at, updated_at
FROM group_reputation WHERE release_group = ?;

-- Every person credited on something, carrying a MusicBrainz artist id and
-- still missing their own image (subject_type='person'). Works have had
-- ListWorksNeedingArtwork driving a dedicated sweep since section 21.6;
-- people never had an equivalent, so an artist resolved before the image code
-- existed stayed without one forever -- the reason no artist picture had ever
-- appeared (found 2026-09-13). 'musicbrainz:artist' must match
-- internal/media/music.ProviderArtist.
-- name: ListPeopleNeedingArtwork :many
SELECT p.id AS person_id, x.external_id AS artist_mbid
FROM person p
JOIN person_external_id x ON x.person_id = p.id AND x.provider = 'musicbrainz:artist'
WHERE NOT EXISTS (
    SELECT 1 FROM artwork a WHERE a.subject_type = 'person' AND a.subject_id = p.id AND a.kind = 'cover'
  )
ORDER BY p.id
LIMIT sqlc.arg(page_size);
