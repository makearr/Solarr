-- Transcoding: what was judged per file, how each kind is doing, and the picks the dispatcher makes. Column lists follow table order.

-- A judged row keeps its attempts, pass and result; a running one is not touched.
-- name: UpsertTranscodeItem :exec
INSERT INTO transcode_item (media_object_id, category_id, kind, state, lane, target_id, setup_revision, reasons_json, plan_json, warnings_json,
                            found_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET
  attempts = CASE WHEN transcode_item.setup_revision <> excluded.setup_revision OR transcode_item.target_id <> excluded.target_id THEN 0 ELSE transcode_item.attempts END,
  pass = CASE WHEN transcode_item.setup_revision <> excluded.setup_revision OR transcode_item.target_id <> excluded.target_id THEN 0 ELSE transcode_item.pass END,
  failure_code = CASE WHEN transcode_item.setup_revision <> excluded.setup_revision OR transcode_item.target_id <> excluded.target_id THEN '' ELSE transcode_item.failure_code END,
  category_id = excluded.category_id,
  kind = excluded.kind,
  state = excluded.state,
  lane = excluded.lane,
  target_id = excluded.target_id,
  setup_revision = excluded.setup_revision,
  reasons_json = excluded.reasons_json,
  plan_json = excluded.plan_json,
  warnings_json = excluded.warnings_json,
  updated_at = excluded.updated_at
WHERE transcode_item.state <> 'running';

-- name: GetTranscodeItem :one
SELECT media_object_id, category_id, kind, state, lane, target_id, setup_revision, reasons_json, plan_json, warnings_json, override_json,
       attempts, pass, interrupted, failure_code, last_error, found_at, last_run_seq, replace_stage, work_path, original_recycle_id,
       original_size_bytes, result_size_bytes, result_ratio_pct, engine, encode_seconds, finished_at, updated_at
FROM transcode_item WHERE media_object_id = ?;

-- The mutable pipeline fields: everything a transition may change except the judged plan and the result.
-- name: SetTranscodeItemState :exec
UPDATE transcode_item SET
  state = ?, lane = ?, override_json = ?, attempts = ?, pass = ?, interrupted = ?, failure_code = ?, last_error = ?,
  last_run_seq = ?, updated_at = ?
WHERE media_object_id = ?;

-- name: SetTranscodeItemResult :exec
UPDATE transcode_item SET
  original_recycle_id = ?, original_size_bytes = ?, result_size_bytes = ?, result_ratio_pct = ?, engine = ?, encode_seconds = ?,
  finished_at = ?, updated_at = ?
WHERE media_object_id = ?;

-- name: SetTranscodeItemJournal :exec
UPDATE transcode_item SET replace_stage = ?, work_path = ?, original_recycle_id = ?, updated_at = ? WHERE media_object_id = ?;

-- Audio and video files under the roots that were never judged, or were judged against another target or setup revision.
-- name: ListObjectsToAnalyze :many
SELECT o.id, o.size_bytes, o.kind, lp.root_folder_id, lp.rel_path
FROM media_object o
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
LEFT JOIN transcode_item ti ON ti.media_object_id = o.id
WHERE o.id > sqlc.arg(after_id)
  AND lp.root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_ids)))
  AND o.kind IN ('video', 'audio')
  AND (ti.media_object_id IS NULL OR ti.target_id <> sqlc.arg(target_id) OR ti.setup_revision <> sqlc.arg(setup_revision))
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- name: PickNeededAlphabetical :many
SELECT ti.media_object_id, ti.lane, ti.last_run_seq, ti.interrupted, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.state = 'needed' AND ti.category_id = ? AND ti.kind = ?
ORDER BY ti.interrupted DESC, lp.rel_path, ti.media_object_id
LIMIT ?;

-- name: PickNeededLargest :many
SELECT ti.media_object_id, ti.lane, ti.last_run_seq, ti.interrupted, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN media_object o ON o.id = ti.media_object_id
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.state = 'needed' AND ti.category_id = ? AND ti.kind = ?
ORDER BY ti.interrupted DESC, o.size_bytes DESC, ti.media_object_id
LIMIT ?;

-- name: PickNeededSmallest :many
SELECT ti.media_object_id, ti.lane, ti.last_run_seq, ti.interrupted, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN media_object o ON o.id = ti.media_object_id
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.state = 'needed' AND ti.category_id = ? AND ti.kind = ?
ORDER BY ti.interrupted DESC, o.size_bytes, ti.media_object_id
LIMIT ?;

-- name: PickNeededOldest :many
SELECT ti.media_object_id, ti.lane, ti.last_run_seq, ti.interrupted, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN media_object o ON o.id = ti.media_object_id
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.state = 'needed' AND ti.category_id = ? AND ti.kind = ?
ORDER BY ti.interrupted DESC, o.first_seen_at, ti.media_object_id
LIMIT ?;

-- name: PickNeededNewest :many
SELECT ti.media_object_id, ti.lane, ti.last_run_seq, ti.interrupted, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN media_object o ON o.id = ti.media_object_id
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.state = 'needed' AND ti.category_id = ? AND ti.kind = ?
ORDER BY ti.interrupted DESC, o.first_seen_at DESC, ti.media_object_id
LIMIT ?;

-- name: PickNeededFound :many
SELECT ti.media_object_id, ti.lane, ti.last_run_seq, ti.interrupted, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.state = 'needed' AND ti.category_id = ? AND ti.kind = ?
ORDER BY ti.interrupted DESC, ti.found_at, ti.media_object_id
LIMIT ?;

-- The far end of the alphabet outside one top-level folder: a probe after failures must not land next to the files that failed.
-- name: PickNeededFarAway :many
SELECT ti.media_object_id, ti.lane, ti.last_run_seq, ti.interrupted, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.state = 'needed' AND ti.category_id = ? AND ti.kind = ?
  AND NOT (lp.root_folder_id = sqlc.arg(avoid_root_id) AND substr(lp.rel_path, 1, length(CAST(sqlc.arg(avoid_prefix) AS TEXT))) = sqlc.arg(avoid_prefix))
ORDER BY lp.rel_path DESC, ti.media_object_id DESC
LIMIT sqlc.arg(page_size);

-- name: ListTranscodeItems :many
SELECT ti.media_object_id, ti.category_id, ti.kind, ti.state, ti.lane, ti.target_id, ti.setup_revision, ti.reasons_json, ti.plan_json,
       ti.warnings_json, ti.override_json, ti.attempts, ti.pass, ti.interrupted, ti.failure_code, ti.last_error, ti.found_at, ti.last_run_seq,
       ti.replace_stage, ti.work_path, ti.original_recycle_id, ti.original_size_bytes, ti.result_size_bytes, ti.result_ratio_pct, ti.engine,
       ti.encode_seconds, ti.finished_at, ti.updated_at, lp.root_folder_id, lp.rel_path
FROM transcode_item ti
JOIN library_path lp ON lp.media_object_id = ti.media_object_id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE ti.media_object_id > sqlc.arg(after_id)
  AND (CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR ti.category_id = sqlc.arg(category_id))
  AND (CAST(sqlc.arg(state) AS TEXT) = '' OR ti.state = sqlc.arg(state))
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR ti.kind = sqlc.arg(kind))
  AND (CAST(sqlc.arg(search) AS TEXT) = '' OR instr(lower(lp.rel_path), lower(CAST(sqlc.arg(search) AS TEXT))) > 0)
ORDER BY ti.media_object_id
LIMIT sqlc.arg(page_size);

-- name: ListRunningTranscodeItems :many
SELECT media_object_id, category_id, kind, work_path, last_run_seq FROM transcode_item WHERE state = 'running' ORDER BY media_object_id;

-- name: ListJournalledTranscodeItems :many
SELECT media_object_id, category_id, kind, replace_stage, work_path, original_recycle_id FROM transcode_item WHERE replace_stage <> '' ORDER BY media_object_id;

-- name: CountFailedTranscodeItemsByCategory :many
SELECT category_id, CAST(COUNT(*) AS INTEGER) AS total FROM transcode_item WHERE state = 'failed' AND target_id = ? GROUP BY category_id;

-- A setup save is a new chance: every failed item of the target starts again.
-- name: ResetFailedTranscodeItems :exec
UPDATE transcode_item SET state = 'needed', attempts = 0, pass = 0, interrupted = 0, failure_code = '', last_error = '', updated_at = ?
WHERE state = 'failed' AND target_id = ?;

-- The truth a counter is corrected against.
-- name: CountTranscodeItemsByState :many
SELECT state, CAST(COUNT(*) AS INTEGER) AS total FROM transcode_item
WHERE CAST(sqlc.arg(category_id) AS INTEGER) = 0 OR category_id = sqlc.arg(category_id)
GROUP BY state;

-- name: GetTranscodeHealth :one
SELECT kind, consecutive_failures, mode, probes_left, backoff_step, backoff_until, last_failed_id, last_error, run_seq
FROM transcode_health WHERE kind = ?;

-- name: UpsertTranscodeHealth :exec
INSERT INTO transcode_health (kind, consecutive_failures, mode, probes_left, backoff_step, backoff_until, last_failed_id, last_error, run_seq)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (kind) DO UPDATE SET
  consecutive_failures = excluded.consecutive_failures,
  mode = excluded.mode,
  probes_left = excluded.probes_left,
  backoff_step = excluded.backoff_step,
  backoff_until = excluded.backoff_until,
  last_failed_id = excluded.last_failed_id,
  last_error = excluded.last_error,
  run_seq = excluded.run_seq;

-- The originals kept in the recycle bin after a conversion, for the size cap.
-- name: SumTranscodeOriginals :one
SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS total FROM recycle_bin WHERE reason = 'transcoded' AND restored_at IS NULL;

-- name: ListOldestTranscodeOriginals :many
SELECT id, media_object_id, size_bytes, recycled_at FROM recycle_bin WHERE reason = 'transcoded' AND restored_at IS NULL ORDER BY recycled_at, id LIMIT ?;

-- What the probe measured of the given files, enough for the coarse analysis; a file never measured has a NULL attribute id.
-- name: ListTranscodeFacts :many
SELECT o.id, o.size_bytes, o.duration_ms, o.width, o.height, o.video_bitrate, o.audio_bitrate, o.overall_bitrate, o.measured_attributes_id,
       a.video_codec, a.hdr, a.audio_codec, a.audio_channels, a.bit_depth, a.languages, a.subtitles, a.frame_rate, a.scan_type, a.container, a.sample_rate
FROM media_object o
LEFT JOIN media_attributes a ON a.id = o.measured_attributes_id
WHERE o.id IN (SELECT value FROM json_each(sqlc.arg(ids)))
ORDER BY o.id;

-- The reasons, plan and warnings a conversion's own analysis or check found, for the item the dispatcher judged coarsely.
-- name: SetTranscodeItemJudgement :exec
UPDATE transcode_item SET reasons_json = ?, plan_json = ?, warnings_json = ?, updated_at = ? WHERE media_object_id = ?;
