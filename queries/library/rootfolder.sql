-- work_root_folder records on-disk presence: which root folder a scan found a work
-- under. It is no longer the sole ADR-27 visibility signal (desired_item.category_id
-- covers a work with no root folder yet, e.g. subscription-discovered), but it is
-- still real and still needed for that half.

-- name: LinkWorkRootFolder :exec
INSERT INTO work_root_folder (work_id, root_folder_id, first_seen_at)
VALUES (?, ?, ?)
ON CONFLICT (work_id, root_folder_id) DO NOTHING;

-- name: ListRootFoldersForWork :many
SELECT root_folder_id FROM work_root_folder WHERE work_id = ? ORDER BY root_folder_id;

-- The "works" count for one category: the union of on-disk presence
-- (work_root_folder) and desired-state membership (desired_item.category_id) --
-- a subscription-discovered work has no root folder yet but still belongs to its
-- category (private/next.md's work_root_folder retirement). Distinct because a
-- scanned, monitored work has rows on both sides and must count once.
-- name: CountWorksVisibleToCategory :one
SELECT CAST(COUNT(DISTINCT work_id) AS INTEGER) AS total FROM (
  SELECT work_id FROM work_root_folder
  WHERE root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
  UNION
  SELECT work_id FROM desired_item WHERE category_id = sqlc.arg(category_id)
);
