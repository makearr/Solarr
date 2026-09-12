-- Download clients (stage 5, ADR-46/57): SABnzbd today, qBittorrent/other torrent clients
-- a later pass. One row per configured connection, the settings.db side
-- acquisition.downloader_id (library.db, migration 0005, built well ahead of this stage) has
-- always been waiting on, no FK (ADR-25).
CREATE TABLE downloader (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,
  impl         TEXT    NOT NULL,      -- "sabnzbd" today; more as later stages need them
  base_url     TEXT    NOT NULL,
  -- credentials is a single sealed secret (secrets.Cipher, the same shape indexer.api_key
  -- already uses) -- sufficient for SABnzbd's own single API key. A torrent client needing a
  -- username/password pair instead is this column's own later design pass, not assumed here.
  credentials  BLOB,
  enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  protocol     TEXT    NOT NULL,      -- "usenet" | "torrent" | "ddl" (ADR-46)
  settings_json TEXT,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
