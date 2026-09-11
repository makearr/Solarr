-- *Arr instance connections (todo 4.13, ADR-52): a first-class import client
-- per application, over the documented REST API and an API key only. app
-- names which client internal/importing/arrimport uses; only "lidarr" is
-- fully wired at stage 4 (music exists) -- the other four connect, preview
-- and park their own intent verbatim until their own stage ships.
-- category_id is where imported intent lands; nullable since a parked app
-- (nothing to land yet) has none. api_key is sealed by secrets.Cipher, never
-- stored in plaintext (Invariant 61) -- the first settings.db column to hold
-- a reversible secret rather than a hash.
CREATE TABLE arr_instance (
  id               INTEGER PRIMARY KEY,
  app              TEXT    NOT NULL CHECK (app IN ('lidarr', 'sonarr', 'radarr', 'readarr', 'whisparr')),
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
