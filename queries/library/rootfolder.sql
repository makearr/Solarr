-- Temporary bridge until desired_item exists for music (see the migration comment).

-- name: LinkWorkRootFolder :exec
INSERT INTO work_root_folder (work_id, root_folder_id, first_seen_at)
VALUES (?, ?, ?)
ON CONFLICT (work_id, root_folder_id) DO NOTHING;

-- name: ListRootFoldersForWork :many
SELECT root_folder_id FROM work_root_folder WHERE work_id = ? ORDER BY root_folder_id;
