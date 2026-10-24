-- Collections, the ready queue, artwork, statistics and reputation.

-- name: CreateCollection :one
INSERT INTO collection (media_type, name, kind, source, monitored, complete_at, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, media_type, name, kind, source, monitored, complete_at, added_at, poster_url, category_id;

-- name: AddCollectionMember :exec
INSERT INTO collection_member (collection_id, work_id, part_id, ord)
VALUES (?, ?, ?, ?)
ON CONFLICT (collection_id, work_id, part_id) DO UPDATE SET ord = excluded.ord;

-- name: ListCollectionMembers :many
SELECT collection_id, work_id, part_id, ord
FROM collection_member WHERE collection_id = ? ORDER BY ord, work_id, part_id;

-- "You have 4 of 6 Ihaol films" is the first thing library-derived discovery
-- looks for, so held and stated counts come back together (ADR-38).
-- name: CollectionCoverage :one
SELECT c.id, c.name, c.complete_at,
       CAST(COUNT(DISTINCT m.work_id) AS INTEGER) AS members,
       CAST(COUNT(DISTINCT CASE WHEN d.state IN ('COMPLETE', 'UPGRADABLE') THEN m.work_id END) AS INTEGER) AS held
FROM collection c
LEFT JOIN collection_member m ON m.collection_id = c.id
LEFT JOIN desired_item d ON d.work_id = m.work_id
WHERE c.id = ?
GROUP BY c.id;

-- name: ListCollectionsForWork :many
SELECT c.id, c.media_type, c.name, c.kind, c.source, c.monitored, c.complete_at, c.added_at, c.poster_url, c.category_id
FROM collection c
JOIN collection_member m ON m.collection_id = c.id
WHERE m.work_id = ? ORDER BY c.name;

-- Primed so something dispatches the instant a slot frees (section 10.5).
-- name: EnqueueReady :exec
INSERT INTO ready_queue (candidate_id, desired_item_id, priority, queued_at, expires_at, size_bytes, origin,
                         indexer_id, guid, title, download_url, protocol, score)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (candidate_id) DO UPDATE SET
  priority = excluded.priority,
  expires_at = excluded.expires_at,
  origin = excluded.origin;

-- Revalidated at dispatch, so an expired entry is never handed to a downloader.
-- name: NextReady :many
SELECT candidate_id, desired_item_id, priority, queued_at, expires_at, size_bytes, origin,
       indexer_id, guid, title, download_url, protocol, score
FROM ready_queue
WHERE expires_at > sqlc.arg(now)
ORDER BY priority DESC, queued_at ASC
LIMIT sqlc.arg(page_size);

-- name: DequeueReady :exec
DELETE FROM ready_queue WHERE candidate_id = ?;

-- One held release per album: a later decision replaces the earlier one.
-- name: DequeueReadyForItem :exec
DELETE FROM ready_queue WHERE desired_item_id = ?;

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
  AND e.work_id > sqlc.arg(after_id)
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

-- What statistics.db's first start copies over (revision 2, D7); every chart reads statistics.db now.
-- name: ListAllStatRollup :many
SELECT metric, dimension, period, value, count, computed_at
FROM stat_rollup
ORDER BY metric, dimension, period;

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
WHERE p.id > sqlc.arg(after_id)
  AND NOT EXISTS (
    SELECT 1 FROM artwork a WHERE a.subject_type = 'person' AND a.subject_id = p.id AND a.kind = 'cover'
  )
ORDER BY p.id
LIMIT sqlc.arg(page_size);

-- Every work still without a cover that carries a release-group id, for the
-- Cover Art Archive's release-group endpoint: a wanted album with no edition
-- has nothing else to look up by, and a release with no art of its own may
-- still have a sibling release that does. MIN picks one id for a work
-- carrying several after a merge. 'musicbrainz:release-group' must match
-- internal/media/music.ProviderReleaseGroup.
-- name: ListWorksNeedingReleaseGroupArtwork :many
SELECT x.work_id AS work_id, CAST(MIN(x.external_id) AS TEXT) AS release_group_mbid
FROM work_external_id x
WHERE x.provider = 'musicbrainz:release-group'
  AND x.work_id > sqlc.arg(after_id)
  AND NOT EXISTS (
    SELECT 1 FROM artwork a WHERE a.subject_type = 'work' AND a.subject_id = x.work_id AND a.kind = 'cover'
  )
GROUP BY x.work_id
ORDER BY x.work_id
LIMIT sqlc.arg(page_size);

-- Every work still without a cover that has a known file to read embedded art
-- from: its lowest-id file, at that file's lowest library_path. Root folder
-- paths live in settings.db, so the caller resolves root_folder_id itself
-- (section 4.3). Only the asking media type's works: a series has no embedded cover.
-- name: ListWorksNeedingEmbeddedArtwork :many
SELECT f.work_id AS work_id, lp.root_folder_id AS root_folder_id, lp.rel_path AS rel_path
FROM work_file f
JOIN library_path lp ON lp.id = (
    SELECT MIN(lp2.id) FROM library_path lp2 WHERE lp2.media_object_id = f.media_object_id
  )
WHERE f.work_id > sqlc.arg(after_id)
  AND EXISTS (SELECT 1 FROM work w WHERE w.id = f.work_id AND w.media_type = sqlc.arg(media_type))
  AND f.media_object_id = (SELECT MIN(f2.media_object_id) FROM work_file f2 WHERE f2.work_id = f.work_id)
  AND NOT EXISTS (
    SELECT 1 FROM artwork a WHERE a.subject_type = 'work' AND a.subject_id = f.work_id AND a.kind = 'cover'
  )
ORDER BY f.work_id
LIMIT sqlc.arg(page_size);

-- A crossover (an event across works) and its members in order, each with what a list shows of it.
-- name: ListPartCollectionMembers :many
SELECT m.collection_id, m.ord, wp.id AS part_id, wp.work_id, wp.media_ref, CAST(COALESCE(wp.title, '') AS TEXT) AS part_title,
       CAST(COALESCE(wp.date, '') AS TEXT) AS date, wp.monitored, w.canonical_title AS work_title,
       EXISTS (SELECT 1 FROM desired_item_file f JOIN desired_item d ON d.id = f.desired_item_id
               JOIN library_path lp ON lp.media_object_id = f.media_object_id AND lp.missing_since IS NULL
               WHERE d.work_id = wp.work_id AND f.media_ref = wp.media_ref AND f.role = 'primary') AS has_file
FROM collection_member m
JOIN work_part wp ON wp.id = m.part_id
JOIN work w ON w.id = wp.work_id
WHERE m.collection_id IN (sqlc.slice(collection_ids))
ORDER BY m.collection_id, m.ord, m.part_id;

-- name: ListCollectionsOfKind :many
SELECT id, media_type, name, kind, source, monitored, complete_at, added_at, poster_url, category_id
FROM collection WHERE kind = sqlc.arg(kind) AND media_type = sqlc.arg(media_type) ORDER BY name, id;

-- name: ListCollectionsOfWorkParts :many
SELECT DISTINCT c.id, c.media_type, c.name, c.kind, c.source, c.monitored, c.complete_at, c.added_at, c.poster_url, c.category_id
FROM collection c JOIN collection_member m ON m.collection_id = c.id
WHERE m.work_id = sqlc.arg(work_id) AND m.part_id <> 0 AND c.kind = sqlc.arg(kind)
ORDER BY c.name, c.id;

-- name: GetCollection :one
SELECT id, media_type, name, kind, source, monitored, complete_at, added_at, poster_url, category_id FROM collection WHERE id = ?;

-- name: RenameCollection :exec
UPDATE collection SET name = ?, monitored = ? WHERE id = ?;

-- name: DeleteCollectionMembers :exec
DELETE FROM collection_member WHERE collection_id = ?;

-- name: DeleteCollection :exec
DELETE FROM collection WHERE id = ?;

-- name: DeleteRelationship :exec
DELETE FROM relationship WHERE from_type = ? AND from_id = ? AND to_type = ? AND to_id = ? AND kind = ?;

-- The parts one part is the same broadcast as, either way round, with what a list shows of them.
-- name: ListSameContentParts :many
SELECT r.from_id AS part_id, wp.id AS other_part_id, wp.work_id AS other_work_id, wp.media_ref AS other_ref,
       w.canonical_title AS other_work_title
FROM relationship r JOIN work_part wp ON wp.id = r.to_id JOIN work w ON w.id = wp.work_id
WHERE r.kind = 'same_content' AND r.from_type = 'part' AND r.to_type = 'part' AND r.from_id IN (sqlc.slice(part_ids))
UNION ALL
SELECT r.to_id AS part_id, wp.id AS other_part_id, wp.work_id AS other_work_id, wp.media_ref AS other_ref,
       w.canonical_title AS other_work_title
FROM relationship r JOIN work_part wp ON wp.id = r.from_id JOIN work w ON w.id = wp.work_id
WHERE r.kind = 'same_content' AND r.from_type = 'part' AND r.to_type = 'part' AND r.to_id IN (sqlc.slice(part_ids_again));

-- Copies one part's coverage onto another's: the same broadcast held once satisfies both.
-- name: CoverPartLikeAnother :exec
INSERT OR IGNORE INTO desired_item_file (desired_item_id, media_object_id, role, media_ref, added_at)
SELECT target_item.id, f.media_object_id, f.role, target.media_ref, f.added_at
FROM work_part source
JOIN desired_item source_item ON source_item.work_id = source.work_id
JOIN desired_item_file f ON f.desired_item_id = source_item.id AND f.media_ref = source.media_ref AND f.role = 'primary'
JOIN work_part target ON target.id = sqlc.arg(target_part_id)
JOIN desired_item target_item ON target_item.work_id = target.work_id AND target_item.satisfaction_kind = 'SET'
  AND (target_item.media_ref = '' OR target.media_ref LIKE target_item.media_ref || ':%')
WHERE source.id = sqlc.arg(source_part_id);

-- Episodes of different works with the same title on the same day, not linked and not ruled out: candidates for one
-- broadcast twice, with what tells them apart (lengths, the people credited on each, how many are the same).
-- name: ListSameContentSuggestions :many
SELECT a.id AS part_id, a.work_id, wa.canonical_title AS work_title, a.media_ref, CAST(COALESCE(a.title, '') AS TEXT) AS title,
       b.id AS other_part_id, b.work_id AS other_work_id, wb.canonical_title AS other_work_title, b.media_ref AS other_ref,
       CAST(COALESCE(a.date, '') AS TEXT) AS date,
       CAST(COALESCE(a.runtime_ms, 0) AS INTEGER) AS runtime_ms, CAST(COALESCE(b.runtime_ms, 0) AS INTEGER) AS other_runtime_ms,
       CAST((SELECT COUNT(DISTINCT ca.person_id) FROM credit ca WHERE ca.part_id = a.id AND ca.part_id != 0) AS INTEGER) AS people,
       CAST((SELECT COUNT(DISTINCT cb.person_id) FROM credit cb WHERE cb.part_id = b.id AND cb.part_id != 0) AS INTEGER) AS other_people,
       CAST((SELECT COUNT(DISTINCT ca.person_id) FROM credit ca JOIN credit cb ON cb.person_id = ca.person_id AND cb.part_id = b.id AND cb.part_id != 0
             WHERE ca.part_id = a.id AND ca.part_id != 0) AS INTEGER) AS shared_people,
       CAST(COALESCE(a.available_at, 0) AS INTEGER) AS available_at, CAST(COALESCE(b.available_at, 0) AS INTEGER) AS other_available_at,
       CAST((SELECT COUNT(*) FROM work_organisation oa JOIN work_organisation ob ON ob.organisation_id = oa.organisation_id AND ob.work_id = b.work_id
             WHERE oa.work_id = a.work_id) AS INTEGER) AS shared_organisations
FROM work_part a
JOIN work_part b ON b.date = a.date AND LOWER(b.title) = LOWER(a.title) AND b.work_id > a.work_id
JOIN work wa ON wa.id = a.work_id
JOIN work wb ON wb.id = b.work_id
WHERE a.date IS NOT NULL AND a.title IS NOT NULL AND LENGTH(a.title) > 3 AND wa.media_type = sqlc.arg(media_type)
  AND wb.media_type = wa.media_type
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = a.work_id AND d.category_id = sqlc.arg(category_id))
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = b.work_id AND d.category_id = sqlc.arg(category_id))
  AND NOT EXISTS (SELECT 1 FROM relationship r WHERE r.kind IN ('same_content', 'different_content')
                  AND ((r.from_id = a.id AND r.to_id = b.id) OR (r.from_id = b.id AND r.to_id = a.id)))
ORDER BY a.date DESC, a.id
LIMIT sqlc.arg(page_size);

-- Every same-broadcast link of a media type, once from each side.
-- name: ListSameContentPairs :many
SELECT a.id AS part_id, a.work_id, a.media_ref, CAST(COALESCE(a.title, '') AS TEXT) AS title,
       CAST(COALESCE(a.date, '') AS TEXT) AS date, b.id AS other_part_id, b.work_id AS other_work_id, b.media_ref AS other_ref,
       CAST(COALESCE((SELECT d.category_id FROM desired_item d WHERE d.work_id = a.work_id ORDER BY d.id LIMIT 1), 0) AS INTEGER) AS category_id
FROM relationship r
JOIN work_part a ON a.id IN (r.from_id, r.to_id)
JOIN work_part b ON b.id IN (r.from_id, r.to_id) AND b.id <> a.id
JOIN work w ON w.id = a.work_id
WHERE r.kind = 'same_content' AND r.from_type = 'part' AND r.to_type = 'part' AND w.media_type = sqlc.arg(media_type)
ORDER BY a.work_id, a.sort_key, b.id;

-- A collection a provider names, found by its id there: a movie's franchise.
-- name: GetCollectionByExternalID :one
SELECT c.id, c.media_type, c.name, c.kind, c.source, c.monitored, c.complete_at, c.added_at, c.poster_url, c.category_id
FROM collection c JOIN collection_external_id e ON e.collection_id = c.id
WHERE e.provider = ? AND e.external_id = ?;

-- name: LinkCollectionExternalID :exec
INSERT INTO collection_external_id (provider, external_id, collection_id) VALUES (?, ?, ?)
ON CONFLICT (provider, external_id) DO NOTHING;

-- name: SetCollectionName :exec
UPDATE collection SET name = ? WHERE id = ?;

-- name: SetCollectionMonitored :exec
UPDATE collection SET monitored = ? WHERE id = ?;

-- name: SetCollectionComplete :exec
UPDATE collection SET complete_at = ? WHERE id = ?;

-- A library's collections: the provider's franchises with a work of the library, and the hand-made ones of the library.
-- name: ListCollectionsInCategory :many
SELECT c.id, c.name, c.kind, c.monitored, c.complete_at AS total, c.poster_url,
       CAST(COALESCE((SELECT e.external_id FROM collection_external_id e WHERE e.collection_id = c.id LIMIT 1), '') AS TEXT) AS external_id
FROM collection c
WHERE c.kind IN ('franchise', 'custom') AND c.media_type = sqlc.arg(media_type)
  AND (c.category_id = sqlc.arg(category_id)
       OR EXISTS (SELECT 1 FROM collection_member m JOIN desired_item d ON d.work_id = m.work_id AND d.category_id = sqlc.arg(category_id)
                  WHERE m.collection_id = c.id))
ORDER BY c.name, c.id;

-- The works of a library in the collections, monitored as the library's own item says, files counted.
-- name: ListCollectionMembersInCategory :many
SELECT m.collection_id, w.id AS work_id, w.canonical_title AS title, CAST(COALESCE(w.year, 0) AS INTEGER) AS year,
       CAST(COALESCE((SELECT MAX(d.monitored) FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id)), 0) AS INTEGER) AS monitored,
       CAST((SELECT COUNT(*) FROM work_file f WHERE f.work_id = w.id) AS INTEGER) AS files,
       CAST(COALESCE((SELECT f.value FROM work_field f WHERE f.work_id = w.id AND f.field = 'images' ORDER BY f.provider LIMIT 1), '') AS TEXT) AS images
FROM collection_member m
JOIN work w ON w.id = m.work_id
WHERE m.collection_id IN (sqlc.slice(collection_ids))
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id))
ORDER BY m.collection_id, m.ord, w.year, w.id;

-- name: CreateCustomCollection :one
INSERT INTO collection (media_type, name, kind, source, monitored, added_at, category_id)
VALUES (?, ?, 'custom', 'user', 0, ?, ?)
RETURNING id;

-- name: RenameCustomCollection :execrows
UPDATE collection SET name = ? WHERE id = ? AND kind = 'custom';

-- name: DeleteCustomCollection :execrows
DELETE FROM collection WHERE id = ? AND kind = 'custom';

-- name: RemoveCollectionMember :exec
DELETE FROM collection_member WHERE collection_id = ? AND work_id = ? AND part_id = 0;

-- name: GetCollectionExternalIDOf :one
SELECT provider, external_id FROM collection_external_id WHERE collection_id = ? ORDER BY provider LIMIT 1;

-- name: SetCollectionPoster :exec
UPDATE collection SET poster_url = ? WHERE id = ?;

-- Every series with the ids Wikidata is asked by.
-- name: ListSeriesProviderIDs :many
SELECT w.id AS work_id,
       CAST(COALESCE((SELECT e.external_id FROM work_external_id e WHERE e.work_id = w.id AND e.provider = 'tvdb' LIMIT 1), '') AS TEXT) AS tvdb,
       CAST(COALESCE((SELECT e.external_id FROM work_external_id e WHERE e.work_id = w.id AND e.provider = 'tmdb' LIMIT 1), '') AS TEXT) AS tmdb
FROM work w
WHERE w.media_type = 'tv'
ORDER BY w.id;

-- Every work of a media type with its name, for finding which belong together.
-- name: ListWorkNamesOfType :many
SELECT w.id AS work_id, w.canonical_title AS title, CAST(COALESCE(w.year, 0) AS INTEGER) AS year
FROM work w WHERE w.media_type = ? ORDER BY w.id;

-- Who belongs to which franchise, of a media type: the provider's and the automatic ones.
-- name: ListFranchiseMembershipsOfType :many
SELECT c.id AS collection_id, c.source AS source, m.work_id
FROM collection c JOIN collection_member m ON m.collection_id = c.id
WHERE c.kind = 'franchise' AND c.media_type = ? AND m.part_id = 0
ORDER BY c.id, m.work_id;

-- Pairs of works a provider says are related (an anime's sequel, its spin-off).
-- name: ListRelatedWorkPairs :many
SELECT r.from_id, r.to_id FROM relationship r
JOIN work a ON a.id = r.from_id AND a.media_type = ?
WHERE r.kind = 'related_series' AND r.from_type = 'work' AND r.to_type = 'work';

-- name: MoveCollectionMembers :exec
INSERT OR IGNORE INTO collection_member (collection_id, work_id, part_id, ord)
SELECT sqlc.arg(into_id), cm.work_id, cm.part_id, cm.ord FROM collection_member cm WHERE cm.collection_id = sqlc.arg(from_id);

-- name: ListGenresOfType :many
SELECT a.subject_id AS work_id, t.name AS genre
FROM tag_assignment a JOIN tag t ON t.id = a.tag_id JOIN work w ON w.id = a.subject_id
WHERE a.subject_type = 'work' AND w.media_type = ? AND t.genre_tree <> '';

-- name: ListOrganisationLinksOfType :many
SELECT wo.work_id, wo.organisation_id FROM work_organisation wo JOIN work w ON w.id = wo.work_id WHERE w.media_type = ?;

-- name: ListCreatorLinksOfType :many
SELECT DISTINCT c.work_id, c.person_id FROM credit c JOIN work w ON w.id = c.work_id
WHERE w.media_type = ? AND c.part_id = 0 AND c.role IN ('creator', 'showrunner');

-- Who plays whom in each work of a media type, once per work: a character two series share makes them one family.
-- name: ListCharacterCreditsOfType :many
SELECT c.work_id, c.person_id, p.name AS person_name, c.character, CAST(MIN(c.part_id) = 0 AS INTEGER) AS regular,
       CAST(COUNT(DISTINCT c.part_id) AS INTEGER) AS appearances,
       CAST(COALESCE(MIN(CASE WHEN c.part_id = 0 THEN c.ord END), -1) AS INTEGER) AS billing
FROM credit c JOIN work w ON w.id = c.work_id JOIN person p ON p.id = c.person_id
WHERE w.media_type = ? AND c.character <> ''
GROUP BY c.work_id, c.person_id, c.character;

-- name: ListFranchisesOfType :many
SELECT c.id, c.name, c.source, c.monitored FROM collection c WHERE c.kind = 'franchise' AND c.media_type = ? ORDER BY c.id;

-- name: ListFranchiseExternalIDsOfType :many
SELECT e.collection_id, e.provider, e.external_id FROM collection_external_id e JOIN collection c ON c.id = e.collection_id
WHERE c.kind = 'franchise' AND c.media_type = ? ORDER BY e.collection_id, e.provider, e.external_id;

-- name: MoveCollectionExternalIDs :exec
UPDATE collection_external_id SET collection_id = sqlc.arg(into_id) WHERE collection_id = sqlc.arg(from_id);

-- Pairs of works a person said do not belong together.
-- name: ListUnrelatedWorkPairs :many
SELECT r.from_id, r.to_id FROM relationship r
JOIN work a ON a.id = r.from_id AND a.media_type = ?
WHERE r.kind = 'unrelated_series' AND r.from_type = 'work' AND r.to_type = 'work';

-- The anime (series with an AniList id) whose relations have not been read yet.
-- One entry per series: the crawl follows sequels and prequels from it to the rest of the franchise.
-- name: ListAnimeWithoutRelationCheck :many
SELECT w.id AS work_id, CAST(MIN(e.external_id) AS TEXT) AS anilist
FROM work w JOIN work_external_id e ON e.work_id = w.id AND e.provider = 'anilist'
WHERE w.media_type = 'tv' AND NOT EXISTS (SELECT 1 FROM work_anime_check c WHERE c.work_id = w.id)
GROUP BY w.id
ORDER BY w.id
LIMIT ?;

-- name: MarkAnimeChecked :exec
INSERT INTO work_anime_check (work_id, checked_at) VALUES (?, ?)
ON CONFLICT (work_id) DO UPDATE SET checked_at = excluded.checked_at;

-- name: ListExternalIDsOfCollection :many
SELECT provider, external_id FROM collection_external_id WHERE collection_id = ? ORDER BY provider, external_id;

-- Episodes of the series in a franchise of two or more series of a library, to find the events that run across them.
-- name: ListFranchiseEpisodes :many
SELECT p.id AS part_id, p.work_id, p.media_ref, CAST(p.title AS TEXT) AS title, CAST(p.date AS TEXT) AS date, f.collection_id AS franchise_id
FROM work_part p
JOIN work w ON w.id = p.work_id
JOIN collection_member f ON f.work_id = p.work_id AND f.part_id = 0
JOIN collection c ON c.id = f.collection_id AND c.kind = 'franchise'
WHERE w.media_type = sqlc.arg(media_type) AND p.title IS NOT NULL AND p.date IS NOT NULL AND LENGTH(p.title) > 6
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = p.work_id AND d.category_id = sqlc.arg(category_id))
  AND (SELECT COUNT(*) FROM collection_member m WHERE m.collection_id = f.collection_id AND m.part_id = 0) > 1;

-- The episodes already in an event of a media type.
-- name: ListCrossoverMemberParts :many
SELECT m.part_id
FROM collection_member m
JOIN collection c ON c.id = m.collection_id
WHERE c.kind = 'crossover' AND c.media_type = ? AND m.part_id != 0;

-- Every pair of episodes a person said are not one broadcast.
-- name: ListDifferentContentPairs :many
SELECT from_id, to_id FROM relationship WHERE kind = 'different_content' AND from_type = 'part' AND to_type = 'part';

-- The works of a media type with the id a provider knows them by, to ask Wikidata which franchise each is part of.
-- name: ListWorkProviderIDs :many
SELECT w.id AS work_id, CAST(e.external_id AS TEXT) AS external_id
FROM work w JOIN work_external_id e ON e.work_id = w.id AND e.provider = sqlc.arg(provider)
WHERE w.media_type = sqlc.arg(media_type)
ORDER BY w.id;
