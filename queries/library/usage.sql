-- What still names a settings row, asked before the settings row is deleted. Settings cannot see these tables.

-- name: CountRootRecycleEntries :one
SELECT COUNT(*) FROM recycle_bin WHERE root_folder_id = ? AND restored_at IS NULL;

-- name: CountRootIdentificationQueue :one
SELECT COUNT(*) FROM identification_queue WHERE root_folder_id = ?;

-- name: CountItemsPinnedToProfile :one
SELECT CAST((SELECT COUNT(*) FROM desired_item d WHERE d.profile_id = sqlc.arg(wanted_id))
          + (SELECT COUNT(*) FROM desired_profile_override o WHERE o.profile_id = sqlc.arg(wanted_id)) AS INTEGER) AS pinned;

-- name: CountItemsPinnedToTarget :one
SELECT COUNT(*) FROM desired_target_override WHERE target_id = ?;

-- name: CountCategoryFilesOnRoot :one
SELECT COUNT(*) FROM (
  SELECT 1 FROM library_path lp
  JOIN desired_item_file f ON f.media_object_id = lp.media_object_id
  JOIN desired_item d ON d.id = f.desired_item_id
  WHERE lp.root_folder_id = sqlc.arg(root_folder_id) AND lp.missing_since IS NULL AND d.category_id = sqlc.arg(category_id)
  LIMIT 100000);
