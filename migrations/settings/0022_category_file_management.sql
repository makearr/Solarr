-- Sonarr's File Management and Permissions, per library (answered 2026-09-24). No row means
-- Sonarr's own defaults. The recycling bin is Solarr's quarantine, so only its retention is here.
CREATE TABLE category_file_management (
  category_id                 INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE,
  propers_repacks             TEXT    NOT NULL DEFAULT 'prefer_and_upgrade'
                              CHECK (propers_repacks IN ('prefer_and_upgrade', 'do_not_upgrade', 'do_not_prefer')),
  rescan_after_refresh        TEXT    NOT NULL DEFAULT 'always'
                              CHECK (rescan_after_refresh IN ('always', 'after_manual', 'never')),
  change_file_date            TEXT    NOT NULL DEFAULT 'none'
                              CHECK (change_file_date IN ('none', 'release_date')),
  recycle_retention_days      INTEGER NOT NULL DEFAULT 7 CHECK (recycle_retention_days BETWEEN 1 AND 365),
  delete_empty_folders        INTEGER NOT NULL DEFAULT 0 CHECK (delete_empty_folders IN (0, 1)),
  create_empty_artist_folders INTEGER NOT NULL DEFAULT 0 CHECK (create_empty_artist_folders IN (0, 1)),
  unmonitor_deleted           INTEGER NOT NULL DEFAULT 0 CHECK (unmonitor_deleted IN (0, 1)),
  set_permissions             INTEGER NOT NULL DEFAULT 0 CHECK (set_permissions IN (0, 1)),
  chmod_folder                TEXT    NOT NULL DEFAULT '755',
  chown_group                 TEXT    NOT NULL DEFAULT ''
);
