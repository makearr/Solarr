-- How fast each download client finished a download, so the Clients page can say what it manages (F34).
CREATE TABLE download_speed (
  id            INTEGER PRIMARY KEY,
  at            INTEGER NOT NULL,
  -- No foreign key: the client lives in settings.db.
  downloader_id INTEGER NOT NULL,
  size_bytes    INTEGER NOT NULL,
  seconds       INTEGER NOT NULL
);
CREATE INDEX download_speed_client_at ON download_speed(downloader_id, at);
CREATE INDEX download_speed_at ON download_speed(at);
