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

-- A held covering part stands for the covered one (a collected edition for the issues it collects); source is a provider key or 'hand'.
CREATE TABLE part_coverage (
  covering_part_id INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  covered_part_id  INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  source           TEXT    NOT NULL,
  PRIMARY KEY (covering_part_id, covered_part_id)
) WITHOUT ROWID;
CREATE INDEX part_coverage_by_covered ON part_coverage(covered_part_id);

-- Webhook deliveries still to make; webhook_id has no foreign key, settings.db is another file.
CREATE TABLE webhook_pending (
  id         INTEGER PRIMARY KEY,
  webhook_id INTEGER NOT NULL,
  event      TEXT    NOT NULL,
  payload    TEXT    NOT NULL,
  queued_at  INTEGER NOT NULL,
  attempts   INTEGER NOT NULL DEFAULT 0,
  next_at    INTEGER NOT NULL
);
CREATE INDEX webhook_pending_due ON webhook_pending(next_at, id);

-- An episode's own IMDb score, filled by OMDb's season listing; null until a refresh finds one.
ALTER TABLE work_part ADD COLUMN rating REAL;
ALTER TABLE work_part ADD COLUMN votes INTEGER;
