-- Desired state. Column lists follow table order, or sqlc stops reusing models.

-- name: CreateDesiredItem :one
INSERT INTO desired_item (work_id, edition_id, media_ref, media_sort_key, category_id,
                          profile_id, monitored, satisfaction_kind, required_count,
                          present_count, state, available_from, available_confidence,
                          available_basis, dirty, generation, last_planned_at, added_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id,
          monitored, satisfaction_kind, required_count, present_count, state,
          available_from, available_confidence, available_basis, dirty, generation,
          last_planned_at, added_by;

-- name: GetDesiredItem :one
SELECT id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id,
       monitored, satisfaction_kind, required_count, present_count, state,
       available_from, available_confidence, available_basis, dirty, generation,
       last_planned_at, added_by
FROM desired_item WHERE id = ?;

-- name: ListDesiredItemsForWork :many
SELECT id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id,
       monitored, satisfaction_kind, required_count, present_count, state,
       available_from, available_confidence, available_basis, dirty, generation,
       last_planned_at, added_by
FROM desired_item WHERE work_id = ? ORDER BY media_sort_key, id;

-- The planner reads the dirty set, not the whole library (ADR-6).
-- name: ListDirtyDesiredItems :many
SELECT id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id,
       monitored, satisfaction_kind, required_count, present_count, state,
       available_from, available_confidence, available_basis, dirty, generation,
       last_planned_at, added_by
FROM desired_item
WHERE dirty = 1 AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: MarkDesiredItemPlanned :exec
UPDATE desired_item SET dirty = 0, generation = generation + 1, last_planned_at = ?
WHERE id = ? AND generation = sqlc.arg(generation);

-- name: MarkDesiredItemDirty :exec
UPDATE desired_item SET dirty = 1 WHERE id = ?;

-- name: SetDesiredItemMonitored :exec
UPDATE desired_item SET monitored = ?, dirty = 1 WHERE id = ?;

-- Re-derived after enrichment adds work_part rows, or a per-track monitor
-- toggle changes how many of them count (Appendix E, D3's shape, one level up).
-- name: SetDesiredItemRequiredCount :exec
UPDATE desired_item SET required_count = ?, dirty = 1 WHERE id = ?;

-- name: SetDesiredItemState :exec
UPDATE desired_item SET state = ?, present_count = ?, dirty = 1 WHERE id = ?;

-- name: SetDesiredItemAvailability :exec
UPDATE desired_item
SET available_from = ?, available_confidence = ?, available_basis = ?, dirty = 1
WHERE id = ?;

-- name: AttachFile :exec
INSERT INTO desired_item_file (desired_item_id, media_object_id, role, media_ref, added_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (desired_item_id, media_object_id, role, media_ref) DO NOTHING;

-- Removes the file from the item entirely, every part it covered with it.
-- name: DetachFile :exec
DELETE FROM desired_item_file
WHERE desired_item_id = ? AND media_object_id = ? AND role = ?;

-- Removes one part's coverage, for a file that turned out to cover less than claimed.
-- name: DetachFilePart :exec
DELETE FROM desired_item_file
WHERE desired_item_id = ? AND media_object_id = ? AND role = ? AND media_ref = ?;

-- name: ListFilesForDesiredItem :many
SELECT desired_item_id, media_object_id, role, media_ref, added_at
FROM desired_item_file WHERE desired_item_id = ? ORDER BY role, media_ref, media_object_id;

-- What a file belongs to. A scan needs this to recognise bytes it has seen.
-- name: ListDesiredItemsForFile :many
SELECT desired_item_id, media_object_id, role, media_ref, added_at
FROM desired_item_file WHERE media_object_id = ? ORDER BY desired_item_id;

-- Which part of a work has a file, and what that file measures. The same
-- media_ref present_count is derived from, so the album detail and the
-- artist overview cannot disagree about what is there; a file a sweep found gone is not. A file at several paths (one
-- broadcast under two series) shows the one in the same top folder as the work's other files.
-- name: ListPartFilesForWork :many
SELECT f.media_ref, f.media_object_id, o.overall_bitrate, o.audio_bitrate,
       a.audio_codec, a.video_codec, a.container, a.lossless, a.languages, a.lyrics_languages,
       o.size_bytes, o.video_bitrate, a.resolution, a.hdr, a.audio_channels, a.subtitles, a.frame_rate, a.crf,
       CAST(COALESCE((SELECT lp.rel_path FROM library_path lp WHERE lp.media_object_id = o.id
                      ORDER BY EXISTS (SELECT 1 FROM work_file wf JOIN library_path sib ON sib.media_object_id = wf.media_object_id
                                       WHERE wf.work_id = d.work_id AND wf.media_object_id <> o.id
                                         AND sib.root_folder_id = lp.root_folder_id AND instr(lp.rel_path, '/') > 0
                                         AND substr(sib.rel_path, 1, instr(lp.rel_path, '/')) = substr(lp.rel_path, 1, instr(lp.rel_path, '/'))) DESC,
                               lp.is_primary DESC, lp.id LIMIT 1), '') AS TEXT) AS rel_path
FROM desired_item d
JOIN desired_item_file f ON f.desired_item_id = d.id
JOIN media_object o ON o.id = f.media_object_id
LEFT JOIN media_attributes a ON a.id = o.measured_attributes_id
WHERE d.work_id = ? AND f.role = 'primary' AND f.media_ref <> ''
  AND NOT (EXISTS (SELECT 1 FROM library_path gone WHERE gone.media_object_id = f.media_object_id AND gone.missing_since IS NOT NULL)
           AND NOT EXISTS (SELECT 1 FROM library_path held WHERE held.media_object_id = f.media_object_id AND held.missing_since IS NULL))
ORDER BY f.media_ref, f.media_object_id;

-- Only 'primary' counts: a trailer belongs to the work without satisfying it.
-- name: CountPrimaryFiles :one
SELECT COUNT(DISTINCT media_ref) AS covered
FROM desired_item_file WHERE desired_item_id = ? AND role = 'primary';

-- The reconciliation job compares this against present_count. Drift is
-- inevitable, so the repair path is not optional. A file whose every path a sweep found gone holds nothing.
-- name: RecomputePresentCount :exec
UPDATE desired_item
SET present_count = (
      SELECT COUNT(DISTINCT f.media_ref)
      FROM desired_item_file f
      WHERE f.desired_item_id = desired_item.id AND f.role = 'primary'
        AND NOT (EXISTS (SELECT 1 FROM library_path gone WHERE gone.media_object_id = f.media_object_id
                           AND gone.missing_since IS NOT NULL)
                 AND NOT EXISTS (SELECT 1 FROM library_path held WHERE held.media_object_id = f.media_object_id
                                   AND held.missing_since IS NULL)))
WHERE desired_item.id = ?;

-- name: SetCounter :exec
INSERT INTO counter (scope, metric, value, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (scope, metric) DO UPDATE SET
  value = excluded.value,
  updated_at = excluded.updated_at;

-- excluded.value is the delta: sqlc cannot substitute a named argument inside DO UPDATE.
-- name: AdjustCounter :exec
INSERT INTO counter (scope, metric, value, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (scope, metric) DO UPDATE SET
  value = counter.value + excluded.value,
  updated_at = excluded.updated_at;

-- A dashboard figure is one lookup regardless of library size (ADR-28).
-- name: GetCounter :one
SELECT scope, metric, value, updated_at FROM counter WHERE scope = ? AND metric = ?;

-- name: ListCountersForScope :many
SELECT scope, metric, value, updated_at FROM counter WHERE scope = ? ORDER BY metric;

-- The truth the reconciliation job compares a counter against.
-- name: CountDesiredItemsByState :many
SELECT state, COUNT(*) AS total FROM desired_item
WHERE monitored = 1
  AND (CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR category_id = sqlc.arg(category_id))
GROUP BY state;

-- name: UpsertHuntQueueEntry :exec
INSERT INTO hunt_queue (desired_item_id, class, priority, last_searched_at,
                        release_date, eligible_after, search_count)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (desired_item_id) DO UPDATE SET
  class = excluded.class,
  priority = excluded.priority,
  release_date = excluded.release_date,
  eligible_after = excluded.eligible_after;

-- When each of a work's desired items was last searched, for the album
-- detail's own "last searched" column. Absent means never.
-- name: ListHuntSearchTimesForWork :many
SELECT h.desired_item_id, h.last_searched_at
FROM hunt_queue h
JOIN desired_item d ON d.id = h.desired_item_id
WHERE d.work_id = ?
ORDER BY h.desired_item_id;

-- Every search records when it ran, whoever started it (Wanted's "last searched"). The planner's
-- own upsert sets priority and class and never touches these two columns.
-- name: RecordItemSearched :exec
INSERT INTO hunt_queue (desired_item_id, class, priority, last_searched_at, search_count)
VALUES (?, 'missing', 0, ?, 1)
ON CONFLICT (desired_item_id) DO UPDATE SET
  last_searched_at = excluded.last_searched_at,
  search_count = hunt_queue.search_count + 1;

-- name: RecordItemDownloadFailed :exec
INSERT INTO hunt_queue (desired_item_id, class, priority, last_searched_at, search_count, after_failure)
VALUES (?, 'missing', 0, 0, 0, 1)
ON CONFLICT (desired_item_id) DO UPDATE SET after_failure = 1;

-- name: RemoveFromHuntQueue :exec
DELETE FROM hunt_queue WHERE desired_item_id = ?;

-- The hunt's five modes (section 9.3), each within a class and, for the new-release and gap shares, a release-date range:
-- release_from and release_before are Unix seconds, zero for no bound; an unknown release date counts as a gap.

-- oldest_searched, the default.
-- name: NextHuntByOldestSearched :many
SELECT desired_item_id, class, priority, last_searched_at, release_date,
       eligible_after, search_count, queued_since, after_failure
FROM hunt_queue
WHERE class = sqlc.arg(class) AND eligible_after <= sqlc.arg(now)
  AND (CAST(sqlc.arg(release_from) AS INTEGER) = 0 OR release_date >= sqlc.arg(release_from))
  AND (CAST(sqlc.arg(release_before) AS INTEGER) = 0 OR release_date IS NULL OR release_date < sqlc.arg(release_before))
ORDER BY priority DESC, last_searched_at ASC
LIMIT sqlc.arg(page_size);

-- newest_searched.
-- name: NextHuntByNewestSearched :many
SELECT desired_item_id, class, priority, last_searched_at, release_date,
       eligible_after, search_count, queued_since, after_failure
FROM hunt_queue
WHERE class = sqlc.arg(class) AND eligible_after <= sqlc.arg(now)
  AND (CAST(sqlc.arg(release_from) AS INTEGER) = 0 OR release_date >= sqlc.arg(release_from))
  AND (CAST(sqlc.arg(release_before) AS INTEGER) = 0 OR release_date IS NULL OR release_date < sqlc.arg(release_before))
ORDER BY priority DESC, last_searched_at DESC
LIMIT sqlc.arg(page_size);

-- oldest_release.
-- name: NextHuntByOldestRelease :many
SELECT desired_item_id, class, priority, last_searched_at, release_date,
       eligible_after, search_count, queued_since, after_failure
FROM hunt_queue
WHERE class = sqlc.arg(class) AND eligible_after <= sqlc.arg(now)
  AND (CAST(sqlc.arg(release_from) AS INTEGER) = 0 OR release_date >= sqlc.arg(release_from))
  AND (CAST(sqlc.arg(release_before) AS INTEGER) = 0 OR release_date IS NULL OR release_date < sqlc.arg(release_before))
ORDER BY priority DESC, release_date ASC
LIMIT sqlc.arg(page_size);

-- newest_release.
-- name: NextHuntByNewestRelease :many
SELECT desired_item_id, class, priority, last_searched_at, release_date,
       eligible_after, search_count, queued_since, after_failure
FROM hunt_queue
WHERE class = sqlc.arg(class) AND eligible_after <= sqlc.arg(now)
  AND (CAST(sqlc.arg(release_from) AS INTEGER) = 0 OR release_date >= sqlc.arg(release_from))
  AND (CAST(sqlc.arg(release_before) AS INTEGER) = 0 OR release_date IS NULL OR release_date < sqlc.arg(release_before))
ORDER BY priority DESC, release_date DESC
LIMIT sqlc.arg(page_size);

-- The composite priority a held release goes out in (ADR-36).
-- name: GetHuntPriority :one
SELECT priority FROM hunt_queue WHERE desired_item_id = ?;

-- name: MarkHunted :exec
UPDATE hunt_queue
SET last_searched_at = ?, search_count = search_count + 1, eligible_after = ?
WHERE desired_item_id = ?;

-- Aged decision detail (section 5.7, Part VI Q5).

-- name: RollUpDecisions :exec
INSERT INTO decision_rollup (desired_item_id, period, outcome, count, best_score, top_reason)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (desired_item_id, period, outcome) DO UPDATE SET
  count = decision_rollup.count + excluded.count,
  best_score = MAX(COALESCE(decision_rollup.best_score, excluded.best_score), excluded.best_score),
  top_reason = excluded.top_reason;

-- name: ListDecisionRollup :many
SELECT desired_item_id, period, outcome, count, best_score, top_reason
FROM decision_rollup
WHERE desired_item_id = ?
ORDER BY period DESC, outcome;

-- name: CountRollupOutcomes :many
SELECT outcome, CAST(SUM(count) AS INTEGER) AS total
FROM decision_rollup
WHERE period >= sqlc.arg(since)
GROUP BY outcome
ORDER BY outcome;

-- After a restore the planner has to re-evaluate everything (ADR-15). Batched so no
-- single transaction exceeds the write budget (Invariant 2), and self-terminating:
-- it stops reporting rows once nothing is left clean.
-- name: MarkDesiredItemsDirtyBatch :execrows
UPDATE desired_item SET dirty = 1
WHERE id IN (SELECT d.id FROM desired_item d WHERE d.dirty = 0 LIMIT sqlc.arg(batch_size));

-- library.db references settings.db by id with no foreign key (ADR-25), so only the
-- application can find a reference a restore has orphaned. The count is what makes
-- the report worth reading.
-- name: CountDesiredItemsByCategory :many
SELECT category_id, CAST(count(*) AS INTEGER) AS total
FROM desired_item GROUP BY category_id ORDER BY category_id;

-- Library health's own "missing tracks" list (todo 4.8): every monitored
-- item still short of its own required_count, using desired_by_category's
-- own (category_id, state) index. UPGRADABLE is deliberately excluded --
-- that item already has everything it needs, just not at the quality
-- wanted, a different report from "missing" entirely.
-- name: ListIncompleteDesiredItems :many
SELECT d.id, d.work_id, w.canonical_title, d.category_id, d.state,
       d.required_count, d.present_count
FROM desired_item d
JOIN work w ON w.id = d.work_id
WHERE d.monitored = 1 AND d.state IN ('MISSING', 'PARTIAL')
  -- An album whose tracks are not known yet may be complete on disk: never searched for (2026-09-28).
  AND EXISTS (SELECT 1 FROM work_part tracklist WHERE tracklist.work_id = d.work_id)
  AND (CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR d.category_id = sqlc.arg(category_id))
  AND (CAST(sqlc.arg(person_id) AS INTEGER) = 0 OR EXISTS (
    SELECT 1 FROM credit c WHERE c.work_id = d.work_id AND c.person_id = sqlc.arg(person_id)))
  AND d.id > sqlc.arg(after_id)
ORDER BY d.id
LIMIT sqlc.arg(page_size);

-- Monitored albums below their library's cutoff: what the Cutoff Unmet search walks.
-- name: ListUpgradableDesiredItems :many
SELECT d.id, d.work_id, w.canonical_title, d.category_id, d.state,
       d.required_count, d.present_count
FROM desired_item d
JOIN work w ON w.id = d.work_id
WHERE d.monitored = 1 AND d.state = 'UPGRADABLE'
  AND (CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR d.category_id = sqlc.arg(category_id))
  AND (CAST(sqlc.arg(person_id) AS INTEGER) = 0 OR EXISTS (
    SELECT 1 FROM credit c WHERE c.work_id = d.work_id AND c.person_id = sqlc.arg(person_id)))
  AND d.id > sqlc.arg(after_id)
ORDER BY d.id
LIMIT sqlc.arg(page_size);

-- Every desired item for one work, however many profiles target it (F33
-- widened the key). Removing an album is removing the intent to have it.
-- A download's record outlives the want it served: history keeps its own copy of the names.
-- name: DetachAcquisitionsForWork :exec
UPDATE acquisition SET desired_item_id = NULL
WHERE desired_item_id IN (SELECT id FROM desired_item WHERE work_id = ?);

-- Import sessions and packs are history too: they keep their rows, no longer pointing at the album.
-- name: DetachImportUnitsFromWork :exec
UPDATE import_unit SET work_id = NULL, edition_id = NULL
WHERE import_unit.work_id = sqlc.arg(work_id)
   OR import_unit.edition_id IN (SELECT e.id FROM edition e WHERE e.work_id = sqlc.arg(work_id));

-- name: DetachPackItemsFromWork :exec
UPDATE pack_item SET work_id = NULL, edition_id = NULL
WHERE pack_item.work_id = sqlc.arg(work_id)
   OR pack_item.edition_id IN (SELECT e.id FROM edition e WHERE e.work_id = sqlc.arg(work_id));

-- name: DeleteDesiredItemsForWork :exec
DELETE FROM desired_item WHERE work_id = ?;

-- A library's "Unmonitor Deleted Tracks": items holding a file a full scan just found gone.
-- name: UnmonitorDesiredItemsWithMissingFiles :execrows
UPDATE desired_item SET monitored = 0, dirty = 1
WHERE id IN (
  SELECT di.id FROM desired_item di
  JOIN desired_item_file dif ON dif.desired_item_id = di.id
  JOIN library_path p ON p.media_object_id = dif.media_object_id
  WHERE di.monitored = 1 AND di.category_id = ? AND p.root_folder_id = ? AND p.missing_since = ?
  ORDER BY di.id
  LIMIT ?
);

-- name: UpsertFileTrackClaim :exec
INSERT INTO file_track_claim (media_object_id, disc, track, title, recording_mbid)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET
  disc = excluded.disc, track = excluded.track, title = excluded.title,
  recording_mbid = excluded.recording_mbid;

-- A work's files with where each claims to sit, for aligning them once its tracklist exists.
-- name: ListFileTrackClaimsForWork :many
SELECT c.media_object_id, c.disc, c.track, c.title, c.recording_mbid
FROM work_file f
JOIN file_track_claim c ON c.media_object_id = f.media_object_id
WHERE f.work_id = ?
ORDER BY c.media_object_id;

-- An item's release date of the given kind, empty when unknown: held releases rank new before backfill.
-- name: GetItemReleaseDate :one
SELECT CAST(COALESCE((SELECT MIN(wd.date) FROM work_date wd
  WHERE wd.work_id = di.work_id AND wd.kind = sqlc.arg(kind)), '') AS TEXT) AS release_date
FROM desired_item di WHERE di.id = sqlc.arg(item_id);

-- The planner's facts for one page of items (section 9.1): the dirty set, or with dirty_only 0
-- the paced sweep. Absent hunt columns read as zero.
-- name: ListPlanInputs :many
SELECT di.id, di.category_id, di.media_ref, di.monitored, di.satisfaction_kind, di.state, di.generation,
       CAST(COALESCE((SELECT MIN(wd.date) FROM work_date wd
                      WHERE wd.work_id = di.work_id AND wd.kind = sqlc.arg(kind)), '') AS TEXT) AS release_date,
       CAST(COALESCE(h.last_searched_at, 0) AS INTEGER) AS last_searched_at,
       CAST(COALESCE(h.search_count, 0) AS INTEGER) AS search_count,
       CAST(COALESCE(h.queued_since, 0) AS INTEGER) AS queued_since,
       CAST(COALESCE(h.after_failure, 0) AS INTEGER) AS after_failure,
       CAST(COALESCE((SELECT a.arrived_at FROM work_arrival a WHERE a.work_id = di.work_id), 0) AS INTEGER) AS arrived_at,
       CAST((SELECT COUNT(*) FROM acquisition aq WHERE aq.desired_item_id = di.id AND aq.state = 'IMPORTED'
             AND aq.updated_at >= sqlc.arg(imports_since)) AS INTEGER) AS recent_imports,
       CAST(COALESCE((SELECT rs.seen_at FROM rss_sighting rs WHERE rs.desired_item_id = di.id), 0) AS INTEGER) AS rss_seen_at
FROM desired_item di
LEFT JOIN hunt_queue h ON h.desired_item_id = di.id
WHERE (CAST(sqlc.arg(dirty_only) AS INTEGER) = 0 OR di.dirty = 1) AND di.id > sqlc.arg(after_id)
ORDER BY di.id
LIMIT sqlc.arg(page_size);

-- name: MaxDesiredItemID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) AS max_id FROM desired_item;

-- Clears dirty only when nothing the plan read has changed since; zero rows means plan it again.
-- name: MarkDesiredItemPlannedIfUnchanged :execrows
UPDATE desired_item SET dirty = 0, generation = generation + 1, last_planned_at = ?
WHERE id = ? AND generation = ? AND state = ? AND monitored = ?;

-- The planner owns every column but the search record, which each search keeps itself.
-- name: UpsertPlannedHunt :exec
INSERT INTO hunt_queue (desired_item_id, class, priority, last_searched_at, release_date,
                        eligible_after, search_count, queued_since)
VALUES (?, ?, ?, 0, ?, ?, 0, ?)
ON CONFLICT (desired_item_id) DO UPDATE SET
  class = excluded.class,
  priority = excluded.priority,
  release_date = excluded.release_date,
  eligible_after = excluded.eligible_after,
  queued_since = excluded.queued_since;

-- name: SetHuntEligibleAfter :exec
UPDATE hunt_queue SET eligible_after = ? WHERE desired_item_id = ?;


-- RSS sync: the monitored, incomplete albums credited to an artist of this name or alias, ignoring case.
-- name: ListWantedAlbumsByArtistName :many
SELECT DISTINCT di.id
FROM person p
JOIN credit c ON c.person_id = p.id AND c.role = 'artist' AND c.edition_id = 0 AND c.part_id = 0
JOIN desired_item di ON di.work_id = c.work_id
WHERE (p.name = sqlc.arg(name) COLLATE NOCASE
       OR EXISTS (SELECT 1 FROM person_alias a WHERE a.person_id = p.id AND a.alias = sqlc.arg(name) COLLATE NOCASE))
  AND di.monitored = 1 AND di.state IN ('MISSING', 'PARTIAL') AND di.satisfaction_kind = 'SET'
ORDER BY di.id
LIMIT sqlc.arg(page_size);

-- An album's page says when it is next searched, and why not (F2).
-- name: GetHuntEntry :one
SELECT desired_item_id, class, priority, last_searched_at, release_date,
       eligible_after, search_count, queued_since, after_failure
FROM hunt_queue WHERE desired_item_id = ?;

-- ready_queue is bounded (section 10.5), so no index is needed to ask this.
-- name: CountHeldForItem :one
SELECT COUNT(*) AS held FROM ready_queue WHERE desired_item_id = ?;

-- M1d's cutoff check walks every monitored album that has all its tracks, by id.
-- name: ListAlbumItemsForCutoff :many
SELECT id, work_id, category_id, profile_id, state, present_count
FROM desired_item
WHERE monitored = 1 AND satisfaction_kind = 'SET' AND state IN ('COMPLETE', 'UPGRADABLE') AND desired_item.id > sqlc.arg(after_id)
  AND EXISTS (SELECT 1 FROM work w WHERE w.id = desired_item.work_id AND w.media_type = 'music')
ORDER BY desired_item.id
LIMIT sqlc.arg(page_size);

-- Which of an album's files a Solarr download brought in: placed by an import session a download
-- started. Only those can replace an owned file (M1d).
-- name: ListDownloadedFilesForWork :many
SELECT DISTINCT wf.media_object_id
FROM work_file wf
JOIN import_unit_file iuf ON iuf.media_object_id = wf.media_object_id
JOIN import_unit u ON u.id = iuf.unit_id
JOIN import_session s ON s.id = u.session_id
WHERE wf.work_id = ? AND s.acquisition_id IS NOT NULL
ORDER BY wf.media_object_id;

-- A release RSS showed for the item; the newest first-seen is kept.
-- name: RecordRSSSighting :exec
INSERT INTO rss_sighting (desired_item_id, seen_at) VALUES (?, ?)
ON CONFLICT (desired_item_id) DO UPDATE SET seen_at = MAX(rss_sighting.seen_at, excluded.seen_at);

-- The SET items of works with a monitored part whose time or day is recent or still ahead: what a
-- season owes may change, or have changed, without anything being written to it.
-- name: ListItemsWithPartsComingOut :many
SELECT DISTINCT d.id, d.work_id
FROM desired_item d
JOIN work_part p ON p.work_id = d.work_id
WHERE d.satisfaction_kind = 'SET' AND d.id > sqlc.arg(after_id) AND p.monitored = 1
  AND ((p.available_at IS NOT NULL AND p.available_at > sqlc.arg(since))
       OR (p.available_at IS NULL AND p.date IS NOT NULL AND p.date > sqlc.arg(since_date)))
ORDER BY d.id
LIMIT sqlc.arg(page_size);

-- The monitored parts covered only by files a full scan just found gone: deleted on purpose, by
-- a library's "Unmonitor deleted" for a type whose parts are wanted one by one.
-- name: ListMonitoredPartsLosingFiles :many
SELECT DISTINCT wp.id AS part_id, di.work_id
FROM desired_item di
JOIN desired_item_file dif ON dif.desired_item_id = di.id
JOIN library_path p ON p.media_object_id = dif.media_object_id
JOIN work_part wp ON wp.work_id = di.work_id AND wp.media_ref = dif.media_ref
WHERE di.category_id = sqlc.arg(category_id) AND p.root_folder_id = sqlc.arg(root_folder_id)
  AND p.missing_since = sqlc.arg(missing_since) AND wp.monitored = 1 AND wp.id > sqlc.arg(after_id)
  AND NOT EXISTS (
    SELECT 1 FROM desired_item_file other
    JOIN library_path held ON held.media_object_id = other.media_object_id
    WHERE other.desired_item_id = di.id AND other.media_ref = dif.media_ref AND held.missing_since IS NULL)
ORDER BY wp.id
LIMIT sqlc.arg(page_size);

-- A work's release day: its earliest dated release, '' when it has none to the day.
-- name: WorkReleaseDay :one
SELECT CAST(COALESCE(MIN(date), '') AS TEXT) AS day FROM work_date WHERE work_id = ? AND LENGTH(date) = 10;

-- The SET items of works without timed parts whose release-day stamp is missing or has passed, for a re-count.
-- name: ListItemsAwaitingRelease :many
SELECT d.id, d.work_id FROM desired_item d
WHERE d.satisfaction_kind = 'SET' AND d.id > sqlc.arg(after_id)
  AND ((d.available_from IS NULL
        AND EXISTS (SELECT 1 FROM work_date wd WHERE wd.work_id = d.work_id AND LENGTH(wd.date) = 10 AND wd.date > sqlc.arg(today)))
    OR (d.available_basis = 'release' AND d.available_from <= sqlc.arg(now)))
  AND NOT EXISTS (SELECT 1 FROM work_part p WHERE p.work_id = d.work_id AND (p.available_at IS NOT NULL OR COALESCE(p.date, '') <> ''))
ORDER BY d.id
LIMIT sqlc.arg(page_size);
