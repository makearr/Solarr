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
