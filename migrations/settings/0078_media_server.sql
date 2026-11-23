-- Media server connections (Jellyfin, Navidrome, ...): scan push, local metadata, watch state.
CREATE TABLE media_server (
  id            INTEGER PRIMARY KEY,
  kind          TEXT    NOT NULL,
  name          TEXT    NOT NULL UNIQUE,
  base_url      TEXT    NOT NULL,
  username      TEXT    NOT NULL DEFAULT '',
  credential    BLOB    NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  as_metadata   INTEGER NOT NULL DEFAULT 0 CHECK (as_metadata IN (0, 1)),
  push_files    INTEGER NOT NULL DEFAULT 0 CHECK (push_files IN (0, 1)),
  announce      INTEGER NOT NULL DEFAULT 0 CHECK (announce IN (0, 1)),
  sync_playback INTEGER NOT NULL DEFAULT 0 CHECK (sync_playback IN (0, 1)),
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  last_ok_at    INTEGER,
  last_error    TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE media_server_library (
  server_id         INTEGER NOT NULL REFERENCES media_server(id) ON DELETE CASCADE,
  category_id       INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  server_library_id TEXT    NOT NULL,
  server_library    TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (server_id, category_id, server_library_id)
) WITHOUT ROWID;

CREATE INDEX media_server_library_category ON media_server_library(category_id);

CREATE TABLE media_server_path (
  server_id     INTEGER NOT NULL REFERENCES media_server(id) ON DELETE CASCADE,
  solarr_prefix TEXT    NOT NULL,
  server_prefix TEXT    NOT NULL,
  PRIMARY KEY (server_id, solarr_prefix)
) WITHOUT ROWID;

CREATE TABLE media_server_user (
  server_id      INTEGER NOT NULL REFERENCES media_server(id) ON DELETE CASCADE,
  server_user_id TEXT    NOT NULL,
  name           TEXT    NOT NULL,
  user_id        INTEGER REFERENCES app_user(id) ON DELETE SET NULL,
  counted        INTEGER NOT NULL DEFAULT 1 CHECK (counted IN (0, 1)),
  PRIMARY KEY (server_id, server_user_id)
) WITHOUT ROWID;

CREATE INDEX media_server_user_user ON media_server_user(user_id);

-- A comic target's aim: the median page edges (long and short, pixels) and the page image format and quality.
ALTER TABLE quality_target ADD COLUMN page_long INTEGER;
ALTER TABLE quality_target ADD COLUMN page_short INTEGER;
ALTER TABLE quality_target ADD COLUMN image_format TEXT NOT NULL DEFAULT '';
ALTER TABLE quality_target ADD COLUMN image_quality INTEGER;

INSERT INTO quality_target (id, name, media_type, container, tolerance_pct, label, description, builtin, page_long, page_short) VALUES (59, 'CBZ, any page size', 'book', 'cbz', 0, '', 'A CBZ archive of any page size.', 1, NULL, NULL);
INSERT INTO quality_target (id, name, media_type, container, tolerance_pct, label, description, builtin, page_long, page_short) VALUES (60, 'CBZ, SD (1968 x 1280)', 'book', 'cbz', 0, '', 'Smaller pages: a CBZ at least 1968 x 1280 pixels a page.', 1, 1968, 1280);
INSERT INTO quality_target (id, name, media_type, container, tolerance_pct, label, description, builtin, page_long, page_short) VALUES (61, 'CBZ, HD (3056 x 1988)', 'book', 'cbz', 0, 'Comic default', 'The default: a CBZ at least 3056 x 1988 pixels a page, as digital releases are.', 1, 3056, 1988);
INSERT INTO quality_target (id, name, media_type, container, tolerance_pct, label, description, builtin, page_long, page_short) VALUES (62, 'CBZ, manga HD (2400 x 1600)', 'book', 'cbz', 0, 'Manga default', 'Manga volumes as scanned: a CBZ at least 2400 x 1600 pixels a page.', 1, 2400, 1600);

INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (23, 'CBZ preferred', '{"quality_profile":"CBZ preferred","compare":["extension","source","score"],"tiers":{"extension":[["cbz"],["cb7"],["cbr"],["cbt"],["epub"],["pdf"]],"source":[["digital"],["webrip"],["hybrid","c2c"],["scan"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 1, 'comic');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (24, 'Any comic', '{"quality_profile":"Any comic","compare":["extension","score"],"tiers":{"extension":[["cbz"],["cb7"],["cbr"],["cbt"],["epub"],["pdf"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 2, 'comic');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (25, 'High resolution', '{"quality_profile":"High resolution","compare":["source","extension","score"],"tiers":{"source":[["digital"],["webrip"],["hybrid","c2c"],["scan"]],"extension":[["cbz"],["cb7"],["cbr"],["cbt"],["epub"],["pdf"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 3, 'comic');

-- Library settings a media type declares of its own (comics: date type, what a collected edition satisfies, reader files), as a JSON object.
ALTER TABLE category_file_management ADD COLUMN module_options TEXT NOT NULL DEFAULT '{}';

-- What a person set about a provider definition file (metadata, indexer, download, webhook); the file itself is the default.
CREATE TABLE provider_definition_state (
  kind           TEXT    NOT NULL,
  key            TEXT    NOT NULL,
  enabled        INTEGER CHECK (enabled IN (0, 1)),
  approved_hash  TEXT    NOT NULL DEFAULT '',
  approved_hosts TEXT    NOT NULL DEFAULT '[]',
  settings       BLOB,
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (kind, key)
) WITHOUT ROWID;

-- Outgoing webhooks: the address and the secret are sealed, since a chat or push address is itself the credential.
CREATE TABLE webhook (
  id            INTEGER PRIMARY KEY,
  name          TEXT    NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  preset        TEXT    NOT NULL,
  url           BLOB    NOT NULL,
  secret        BLOB,
  events        TEXT    NOT NULL DEFAULT '[]',
  category_ids  TEXT    NOT NULL DEFAULT '[]',
  include_paths INTEGER NOT NULL DEFAULT 0 CHECK (include_paths IN (0, 1)),
  include_adult INTEGER NOT NULL DEFAULT 0 CHECK (include_adult IN (0, 1)),
  last_status   INTEGER,
  last_error    TEXT    NOT NULL DEFAULT '',
  last_at       INTEGER,
  created_at    INTEGER NOT NULL
);

-- Incoming webhooks: only the SHA-256 of the token is kept, so the token is shown once. media_server_id has no foreign key (a connection delete is refused elsewhere).
CREATE TABLE webhook_inbound (
  id              INTEGER PRIMARY KEY,
  name            TEXT    NOT NULL,
  enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  token_hash      TEXT    NOT NULL,
  actions         TEXT    NOT NULL DEFAULT '[]',
  category_ids    TEXT    NOT NULL DEFAULT '[]',
  media_server_id INTEGER NOT NULL DEFAULT 0,
  last_used_at    INTEGER,
  created_at      INTEGER NOT NULL
);

-- Mylar3 and Kapowarr join the apps an arr_instance can name; a CHECK cannot be altered, so the table is rebuilt as it was.
CREATE TABLE arr_instance_rebuilt (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  app              TEXT    NOT NULL CHECK (app IN ('lidarr', 'sonarr', 'radarr', 'readarr', 'whisparr', 'prowlarr', 'mylar3', 'kapowarr')),
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
INSERT INTO arr_instance_rebuilt (id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at, last_synced_at, last_sync_error, push_monitoring, last_pushed_at, sync_json)
SELECT id, app, name, base_url, api_key, category_id, enabled, created_at, updated_at, last_synced_at, last_sync_error, push_monitoring, last_pushed_at, sync_json FROM arr_instance;
DROP TABLE arr_instance;
ALTER TABLE arr_instance_rebuilt RENAME TO arr_instance;

-- Discover (recommendations): one row per library that has ever been configured; a missing row is the defaults with Discover off.
CREATE TABLE category_discover (
  category_id        INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE,
  enabled            INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  total_amount       INTEGER NOT NULL DEFAULT 60 CHECK (total_amount BETWEEN 1 AND 500),
  classic_age_years  INTEGER NOT NULL DEFAULT 25 CHECK (classic_age_years BETWEEN 1 AND 150),
  min_rating         REAL    NOT NULL DEFAULT 7.0 CHECK (min_rating BETWEEN 0 AND 10),
  min_votes          INTEGER NOT NULL DEFAULT 1000 CHECK (min_votes >= 0),
  auto_add_per_day   INTEGER NOT NULL DEFAULT 5 CHECK (auto_add_per_day BETWEEN 0 AND 100),
  auto_add_search    INTEGER NOT NULL DEFAULT 0 CHECK (auto_add_search IN (0, 1)),
  original_languages TEXT    NOT NULL DEFAULT '',
  languages_set      INTEGER NOT NULL DEFAULT 0 CHECK (languages_set IN (0, 1)),
  exclude_genres     TEXT    NOT NULL DEFAULT '',
  exclude_networks   TEXT    NOT NULL DEFAULT '',
  disabled_kinds     TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE category_discover_type (
  category_id INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  type        TEXT    NOT NULL CHECK (type IN ('highest_rated', 'trending', 'upcoming', 'classics', 'collections')),
  enabled     INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  amount      INTEGER NOT NULL DEFAULT 20 CHECK (amount BETWEEN 1 AND 200),
  auto_add    INTEGER NOT NULL DEFAULT 0 CHECK (auto_add IN (0, 1)),
  PRIMARY KEY (category_id, type)
) WITHOUT ROWID;

-- Discover took over the TMDB chart import lists: a library that had one gets Discover on with trending auto-adding when the list
-- was enabled (popular becomes trending too), then the lists and the unused discovery jobs go. One way.
INSERT INTO category_discover (category_id, enabled, auto_add_search)
SELECT category_id, 1, MAX(search_on_add) FROM import_list WHERE type IN ('tmdb_trending_tv', 'tmdb_popular_tv') GROUP BY category_id;
INSERT INTO category_discover_type (category_id, type, enabled, amount, auto_add)
SELECT category_id, 'trending', 1, 20, MAX(enabled) FROM import_list WHERE type IN ('tmdb_trending_tv', 'tmdb_popular_tv') GROUP BY category_id;
INSERT OR REPLACE INTO instance_setting (key, value_json, updated_at)
SELECT 'discover.enabled', 'true', CAST(strftime('%s', 'now') AS INTEGER) WHERE EXISTS (SELECT 1 FROM import_list WHERE type IN ('tmdb_trending_tv', 'tmdb_popular_tv'));
DELETE FROM import_list WHERE type IN ('tmdb_trending_tv', 'tmdb_popular_tv');
DROP INDEX discovery_job_enabled;
DROP TABLE discovery_job;
