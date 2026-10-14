-- Per-root sweep progress (section 10.4). Column lists follow table order, or sqlc
-- stops reusing models.

-- name: GetScanState :one
SELECT root_folder_id, swept_through, cursor, started_at, finished_at,
       files_found, directories_read, vanished_held, vanished_through
FROM scan_state WHERE root_folder_id = ?;

-- name: ListScanStates :many
SELECT root_folder_id, swept_through, cursor, started_at, finished_at,
       files_found, directories_read, vanished_held, vanished_through
FROM scan_state ORDER BY root_folder_id;

-- A sweep that starts records only that it started: the watermark must not move
-- until one finishes, or an interrupted scan would look complete.
-- name: StartScan :exec
INSERT INTO scan_state (root_folder_id, swept_through, cursor, started_at, finished_at,
                        files_found, directories_read)
VALUES (sqlc.arg(root_folder_id), 0, '', sqlc.arg(started_at), NULL, 0, 0)
ON CONFLICT (root_folder_id) DO UPDATE SET
  started_at = excluded.started_at, finished_at = NULL;

-- Progress mid-sweep: the cursor advances, the watermark does not.
-- name: SetScanCursor :exec
UPDATE scan_state SET cursor = ? WHERE root_folder_id = ?;

-- Only a completed sweep moves the watermark and clears the cursor.
-- name: FinishScan :exec
UPDATE scan_state
SET swept_through = sqlc.arg(swept_through), cursor = '',
    finished_at = sqlc.arg(finished_at),
    files_found = sqlc.arg(files_found),
    directories_read = sqlc.arg(directories_read)
WHERE root_folder_id = sqlc.arg(root_folder_id);

-- A root that is removed from configuration leaves no progress behind.
-- name: DeleteScanState :exec
DELETE FROM scan_state WHERE root_folder_id = ?;

-- A mass vanish is held, not marked: the count and the sweep start a later accept marks against.
-- name: HoldVanished :exec
UPDATE scan_state SET vanished_held = ?, vanished_through = ? WHERE root_folder_id = ?;

-- name: ClearVanished :exec
UPDATE scan_state SET vanished_held = 0, vanished_through = 0 WHERE root_folder_id = ?;
