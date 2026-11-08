-- Proxies an indexer's requests can go through; referenced by id from indexer.settings_json, so no foreign key.
CREATE TABLE proxy (
  id                 INTEGER PRIMARY KEY,
  name               TEXT    NOT NULL UNIQUE,
  kind               TEXT    NOT NULL CHECK (kind IN ('http', 'socks5', 'flaresolverr')),
  url                TEXT    NOT NULL,
  source_arr_id      INTEGER,
  source_external_id INTEGER,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
