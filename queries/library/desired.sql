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
-- artist overview cannot disagree about what is there.
-- name: ListPartFilesForWork :many
SELECT f.media_ref, f.media_object_id, o.overall_bitrate, o.audio_bitrate,
       a.audio_codec, a.video_codec, a.container, a.lossless,
       CAST(COALESCE((SELECT lp.rel_path FROM library_path lp WHERE lp.media_object_id = o.id
                      ORDER BY lp.is_primary DESC, lp.id LIMIT 1), '') AS TEXT) AS rel_path
FROM desired_item d
JOIN desired_item_file f ON f.desired_item_id = d.id
JOIN media_object o ON o.id = f.media_object_id
LEFT JOIN media_attributes a ON a.id = o.measured_attributes_id
WHERE d.work_id = ? AND f.role = 'primary' AND f.media_ref <> ''
ORDER BY f.media_ref, f.media_object_id;

-- Only 'primary' counts: a trailer belongs to the work without satisfying it.
-- name: CountPrimaryFiles :one
SELECT COUNT(DISTINCT media_ref) AS covered
FROM desired_item_file WHERE desired_item_id = ? AND role = 'primary';

-- The reconciliation job compares this against present_count. Drift is
-- inevitable, so the repair path is not optional.
-- name: RecomputePresentCount :exec
UPDATE desired_item
SET present_count = (
      SELECT COUNT(DISTINCT media_ref)
      FROM desired_item_file
      WHERE desired_item_id = desired_item.id AND role = 'primary')
WHERE id = ?;

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

-- name: RemoveFromHuntQueue :exec
DELETE FROM hunt_queue WHERE desired_item_id = ?;

-- oldest_searched, the default hunt mode (section 9.3).
-- name: NextHuntByOldestSearched :many
SELECT desired_item_id, class, priority, last_searched_at, release_date,
       eligible_after, search_count
FROM hunt_queue
WHERE class = sqlc.arg(class) AND eligible_after <= sqlc.arg(now)
ORDER BY priority DESC, last_searched_at ASC
LIMIT sqlc.arg(page_size);

-- name: NextHuntByNewestRelease :many
SELECT desired_item_id, class, priority, last_searched_at, release_date,
       eligible_after, search_count
FROM hunt_queue
WHERE class = sqlc.arg(class) AND eligible_after <= sqlc.arg(now)
ORDER BY priority DESC, release_date DESC
LIMIT sqlc.arg(page_size);

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
  AND (CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR d.category_id = sqlc.arg(category_id))
  AND d.id > sqlc.arg(after_id)
ORDER BY d.id
LIMIT sqlc.arg(page_size);

-- Every desired item for one work, however many profiles target it (F33
-- widened the key). Removing an album is removing the intent to have it.
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
