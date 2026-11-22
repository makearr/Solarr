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
