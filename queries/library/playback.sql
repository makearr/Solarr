-- What media server users have played. Column lists follow table order.

-- name: UpsertPlaybackState :exec
INSERT INTO playback_state (server_id, server_user_id, work_id, part_id, played, play_count, position_ms, duration_ms, last_played_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, server_user_id, work_id, part_id) DO UPDATE SET
  played = excluded.played, play_count = excluded.play_count, position_ms = excluded.position_ms,
  duration_ms = excluded.duration_ms, last_played_at = excluded.last_played_at, updated_at = excluded.updated_at;

-- name: GetPlaybackSince :one
SELECT since FROM playback_sync WHERE server_id = ? AND server_user_id = ?;

-- name: SetPlaybackSince :exec
INSERT INTO playback_sync (server_id, server_user_id, since) VALUES (?, ?, ?)
ON CONFLICT (server_id, server_user_id) DO UPDATE SET since = excluded.since;

-- name: DeletePlaybackSyncOf :exec
DELETE FROM playback_sync WHERE server_id = ?;

-- name: DeletePlaybackStateChunk :execrows
DELETE FROM playback_state WHERE (server_id, server_user_id, work_id, part_id) IN (
  SELECT p.server_id, p.server_user_id, p.work_id, p.part_id FROM playback_state p WHERE p.server_id = ? LIMIT 200);

-- name: DeletePlaybackStateOfUserChunk :execrows
DELETE FROM playback_state WHERE (server_id, server_user_id, work_id, part_id) IN (
  SELECT p.server_id, p.server_user_id, p.work_id, p.part_id FROM playback_state p WHERE p.server_id = ? AND p.server_user_id = ? LIMIT 200);

-- name: ResolvePlaybackFile :one
SELECT wf.work_id,
  CAST(COALESCE((SELECT wp.id FROM desired_item_file dif
    JOIN desired_item di ON di.id = dif.desired_item_id
    JOIN work_part wp ON wp.work_id = di.work_id AND wp.media_ref = COALESCE(NULLIF(dif.media_ref, ''), di.media_ref)
    WHERE dif.media_object_id = lp.media_object_id AND di.work_id = wf.work_id
    ORDER BY wp.id LIMIT 1), 0) AS INTEGER) AS part_id
FROM library_path lp JOIN work_file wf ON wf.media_object_id = lp.media_object_id
WHERE lp.root_folder_id = ? AND lp.rel_path = ?
ORDER BY wf.work_id LIMIT 1;

-- name: ResolvePlaybackWorkByExternalID :one
SELECT work_id FROM work_external_id WHERE provider = ? AND external_id = ?;

-- name: ResolvePlaybackPartByExternalID :one
SELECT wp.work_id, wp.id AS part_id FROM work_part_external_id wpe JOIN work_part wp ON wp.id = wpe.part_id
WHERE wpe.provider = ? AND wpe.external_id = ? ORDER BY wp.id LIMIT 1;
