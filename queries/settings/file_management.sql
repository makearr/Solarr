-- name: ListCategoryFileManagement :many
SELECT category_id, propers_repacks, rescan_after_refresh, change_file_date, recycle_retention_days,
       delete_empty_folders, create_empty_artist_folders, unmonitor_deleted, set_permissions,
       chmod_folder, chown_group, write_ids_to_tags, replace_ids_in_tags, write_metadata_to_tags,
       embed_cover_in_tags, merge_artist_aliases, navidrome_profile, import_extra_files, extra_file_extensions, media_server_files,
       shared_file_fallback, ignore_new_seasons
FROM category_file_management
ORDER BY category_id;

-- name: UpsertCategoryFileManagement :exec
INSERT INTO category_file_management (
  category_id, propers_repacks, rescan_after_refresh, change_file_date, recycle_retention_days,
  delete_empty_folders, create_empty_artist_folders, unmonitor_deleted, set_permissions,
  chmod_folder, chown_group, write_ids_to_tags, replace_ids_in_tags, write_metadata_to_tags,
  embed_cover_in_tags, merge_artist_aliases, navidrome_profile, import_extra_files, extra_file_extensions,
  media_server_files, shared_file_fallback, ignore_new_seasons
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
  write_ids_to_tags = excluded.write_ids_to_tags,
  replace_ids_in_tags = excluded.replace_ids_in_tags,
  write_metadata_to_tags = excluded.write_metadata_to_tags,
  embed_cover_in_tags = excluded.embed_cover_in_tags,
  merge_artist_aliases = excluded.merge_artist_aliases,
  navidrome_profile = excluded.navidrome_profile,
  import_extra_files = excluded.import_extra_files,
  extra_file_extensions = excluded.extra_file_extensions,
  media_server_files = excluded.media_server_files,
  shared_file_fallback = excluded.shared_file_fallback,
  ignore_new_seasons = excluded.ignore_new_seasons;
