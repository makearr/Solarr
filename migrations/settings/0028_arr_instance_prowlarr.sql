-- Prowlarr as a connection (asked 2026-09-30): its active indexers and download clients are used from
-- here. SQLite cannot change a CHECK, so the table is rebuilt with the same columns.
CREATE TABLE arr_instance_next (
  id               INTEGER PRIMARY KEY,
  app              TEXT    NOT NULL CHECK (app IN ('lidarr', 'sonarr', 'radarr', 'readarr', 'whisparr', 'prowlarr')),
  name             TEXT    NOT NULL,
  base_url         TEXT    NOT NULL,
  api_key          BLOB    NOT NULL,
  category_id      INTEGER REFERENCES category(id),
  enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  last_synced_at   INTEGER,
  last_sync_error  TEXT,
  UNIQUE (app, name)
);
INSERT INTO arr_instance_next (id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at,
                               last_synced_at, last_sync_error)
SELECT id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at, last_synced_at, last_sync_error
FROM arr_instance;
DROP TABLE arr_instance;
ALTER TABLE arr_instance_next RENAME TO arr_instance;
