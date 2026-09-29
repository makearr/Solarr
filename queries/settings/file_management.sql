-- name: ListCategoryFileManagement :many
SELECT category_id, propers_repacks, rescan_after_refresh, change_file_date, recycle_retention_days,
       delete_empty_folders, create_empty_artist_folders, unmonitor_deleted, set_permissions,
       chmod_folder, chown_group, write_ids_to_tags
FROM category_file_management
ORDER BY category_id;

-- name: UpsertCategoryFileManagement :exec
INSERT INTO category_file_management (
  category_id, propers_repacks, rescan_after_refresh, change_file_date, recycle_retention_days,
  delete_empty_folders, create_empty_artist_folders, unmonitor_deleted, set_permissions,
  chmod_folder, chown_group, write_ids_to_tags
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id) DO UPDATE SET
  propers_repacks = excluded.propers_repacks,
  rescan_after_refresh = excluded.rescan_after_refresh,
  change_file_date = excluded.change_file_date,
  recycle_retention_days = excluded.recycle_retention_days,
  delete_empty_folders = excluded.delete_empty_folders,
  create_empty_artist_folders = excluded.create_empty_artist_folders,
  unmonitor_deleted = excluded.unmonitor_deleted,
  set_permissions = excluded.set_permissions,
  chmod_folder = excluded.chmod_folder,
  chown_group = excluded.chown_group,
  write_ids_to_tags = excluded.write_ids_to_tags;
