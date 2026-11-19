-- An *Arr connection's id is kept in library.db (links, import runs): a deleted connection's id must never come back.
CREATE TABLE arr_instance_new (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
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
  push_monitoring INTEGER NOT NULL DEFAULT 0 CHECK (push_monitoring IN (0, 1)),
  last_pushed_at INTEGER,
  sync_json TEXT,
  UNIQUE (app, name)
);

INSERT INTO arr_instance_new (id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at,
                              last_synced_at, last_sync_error, push_monitoring, last_pushed_at, sync_json)
SELECT id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at,
       last_synced_at, last_sync_error, push_monitoring, last_pushed_at, sync_json
FROM arr_instance;

DROP TABLE arr_instance;
ALTER TABLE arr_instance_new RENAME TO arr_instance;
