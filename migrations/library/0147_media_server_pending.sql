-- Paths a media server has still to be told about; server_id has no foreign key, settings.db is another file.
CREATE TABLE media_server_pending (
  id          INTEGER PRIMARY KEY,
  server_id   INTEGER NOT NULL,
  category_id INTEGER NOT NULL,
  path        TEXT    NOT NULL,
  change      TEXT    NOT NULL CHECK (change IN ('created', 'modified', 'deleted')),
  work_id     INTEGER NOT NULL DEFAULT 0,
  queued_at   INTEGER NOT NULL,
  attempts    INTEGER NOT NULL DEFAULT 0,
  UNIQUE (server_id, path, change)
);
CREATE INDEX media_server_pending_due ON media_server_pending(server_id, queued_at, id);
