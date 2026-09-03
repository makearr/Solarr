-- Temporary bridge until desired_item exists for music (see the migration comment).

-- name: LinkWorkRootFolder :exec
INSERT INTO work_root_folder (work_id, root_folder_id, first_seen_at)
VALUES (?, ?, ?)
ON CONFLICT (work_id, root_folder_id) DO NOTHING;

-- name: ListRootFoldersForWork :many
SELECT root_folder_id FROM work_root_folder WHERE work_id = ? ORDER BY root_folder_id;

-- The truth a category counter is checked against (library/categorycounts.go): every
-- root folder's work count in one query, summed per category in Go since library.db
-- does not know which root folders a category spans (ADR-25).
-- name: CountWorksByRootFolder :many
SELECT root_folder_id, CAST(COUNT(DISTINCT work_id) AS INTEGER) AS total
FROM work_root_folder GROUP BY root_folder_id ORDER BY root_folder_id;
