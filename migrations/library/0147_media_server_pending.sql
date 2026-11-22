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

-- What each server user has played, by work and part (part 0 is the whole work), so an upgraded file keeps its history.
CREATE TABLE playback_state (
  server_id      INTEGER NOT NULL,
  server_user_id TEXT    NOT NULL,
  work_id        INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  part_id        INTEGER NOT NULL DEFAULT 0,
  played         INTEGER NOT NULL DEFAULT 0 CHECK (played IN (0, 1)),
  play_count     INTEGER NOT NULL DEFAULT 0,
  position_ms    INTEGER NOT NULL DEFAULT 0,
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  last_played_at INTEGER,
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (server_id, server_user_id, work_id, part_id)
) WITHOUT ROWID;
CREATE INDEX playback_state_work ON playback_state(work_id, part_id, played);

-- The newest play already stored per server user: the next sync asks only for what changed since.
CREATE TABLE playback_sync (
  server_id      INTEGER NOT NULL,
  server_user_id TEXT    NOT NULL,
  since          INTEGER NOT NULL,
  PRIMARY KEY (server_id, server_user_id)
) WITHOUT ROWID;
