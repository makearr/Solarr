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

-- Which files the lyrics pass has asked LRCLIB about; found = 0 is asked again after a while, not on every run.
CREATE TABLE lyrics_attempt (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  attempted_at    INTEGER NOT NULL,
  found           INTEGER NOT NULL CHECK (found IN (0, 1))
);

-- Discover (recommendations): the pool is a cache of provider answers after filtering, rebuilt by a job.
CREATE TABLE recommendation (
  id                INTEGER PRIMARY KEY,
  category_id       INTEGER NOT NULL,
  kind              TEXT    NOT NULL,
  provider          TEXT    NOT NULL,
  external_id       TEXT    NOT NULL,
  title             TEXT    NOT NULL,
  subtitle          TEXT    NOT NULL DEFAULT '',
  overview          TEXT    NOT NULL DEFAULT '',
  poster_url        TEXT    NOT NULL DEFAULT '',
  year              INTEGER NOT NULL DEFAULT 0,
  released_on       TEXT    NOT NULL DEFAULT '',
  popularity        REAL    NOT NULL DEFAULT 0,
  rating            REAL    NOT NULL DEFAULT 0,
  votes             INTEGER NOT NULL DEFAULT 0,
  original_language TEXT    NOT NULL DEFAULT '',
  certification     TEXT    NOT NULL DEFAULT '',
  networks_json     TEXT    NOT NULL DEFAULT '',
  work_id           INTEGER NOT NULL DEFAULT 0,
  fetched_at        INTEGER NOT NULL,
  UNIQUE (category_id, kind, provider, external_id)
);
CREATE INDEX recommendation_by_popularity ON recommendation(category_id, kind, popularity DESC, id DESC);

CREATE TABLE recommendation_genre (
  recommendation_id INTEGER NOT NULL REFERENCES recommendation(id) ON DELETE CASCADE,
  genre             TEXT    NOT NULL,
  PRIMARY KEY (recommendation_id, genre)
) WITHOUT ROWID;
CREATE INDEX recommendation_genre_by_genre ON recommendation_genre(genre, recommendation_id);

-- Which list of which source names an item; one item may be named by several sources of one list.
CREATE TABLE recommendation_feed_item (
  recommendation_id INTEGER NOT NULL REFERENCES recommendation(id) ON DELETE CASCADE,
  type              TEXT    NOT NULL,
  source            TEXT    NOT NULL,
  rank              INTEGER NOT NULL,
  PRIMARY KEY (recommendation_id, type, source)
) WITHOUT ROWID;

-- One source's read of one list; popularity_max is the raw popularity a stored 100 stands for.
CREATE TABLE recommendation_feed (
  category_id    INTEGER NOT NULL,
  kind           TEXT    NOT NULL,
  type           TEXT    NOT NULL,
  source         TEXT    NOT NULL,
  pages_read     INTEGER NOT NULL DEFAULT 0,
  exhausted      INTEGER NOT NULL DEFAULT 0 CHECK (exhausted IN (0, 1)),
  refreshed_at   INTEGER NOT NULL DEFAULT 0,
  popularity_max REAL    NOT NULL DEFAULT 0,
  last_error     TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (category_id, kind, type, source)
) WITHOUT ROWID;

-- A person's "not interested", by provider id so it survives a refresh; no foreign key, app_user lives in settings.db.
CREATE TABLE recommendation_dismissal (
  user_id     INTEGER NOT NULL,
  media_type  TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  title       TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (user_id, provider, external_id, kind)
) WITHOUT ROWID;

-- What auto-add added, so a work the person later removes is never added a second time.
CREATE TABLE recommendation_auto_add (
  category_id INTEGER NOT NULL,
  kind        TEXT    NOT NULL,
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  title       TEXT    NOT NULL,
  type        TEXT    NOT NULL,
  rating      REAL    NOT NULL,
  votes       INTEGER NOT NULL,
  work_id     INTEGER NOT NULL DEFAULT 0,
  added_at    INTEGER NOT NULL,
  PRIMARY KEY (category_id, kind, provider, external_id)
) WITHOUT ROWID;
CREATE INDEX recommendation_auto_add_by_day ON recommendation_auto_add(category_id, added_at);

-- One row per audio or video file that transcoding judged; the state is what the pipeline reads and the counters mirror.
CREATE TABLE transcode_item (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  -- No foreign key: the library lives in settings.db.
  category_id     INTEGER NOT NULL,
  kind            TEXT    NOT NULL CHECK (kind IN ('video', 'audio', 'comic')),
  state           TEXT    NOT NULL CHECK (state IN ('needed', 'running', 'done', 'not_needed', 'failed', 'blocked')),
  lane            TEXT    NOT NULL DEFAULT '' CHECK (lane IN ('', 'cpu', 'gpu', 'audio')),
  -- What it was judged against; a change of either makes the row stale.
  target_id       INTEGER NOT NULL,
  setup_revision  INTEGER NOT NULL,
  -- Structured reasons (code, stream, want, have) for the state, the plan of a conversion, and its warnings.
  reasons_json    TEXT    NOT NULL DEFAULT '',
  plan_json       TEXT    NOT NULL DEFAULT '',
  warnings_json   TEXT    NOT NULL DEFAULT '',
  -- A person's retry settings (quality, bitrate, preset, engine); empty is the setup's.
  override_json   TEXT    NOT NULL DEFAULT '',
  attempts        INTEGER NOT NULL DEFAULT 0,
  pass            INTEGER NOT NULL DEFAULT 0,
  interrupted     INTEGER NOT NULL DEFAULT 0 CHECK (interrupted IN (0, 1)),
  failure_code    TEXT    NOT NULL DEFAULT '',
  last_error      TEXT    NOT NULL DEFAULT '',
  -- Order of discovery, and the run that touched it last (never the same item twice in a row).
  found_at        INTEGER NOT NULL,
  last_run_seq    INTEGER NOT NULL DEFAULT 0,
  -- The replace journal: what a restart must finish or undo.
  replace_stage   TEXT    NOT NULL DEFAULT '' CHECK (replace_stage IN ('', 'recycling', 'placing', 'restoring')),
  work_path       TEXT    NOT NULL DEFAULT '',
  -- The result.
  original_recycle_id INTEGER REFERENCES recycle_bin(id) ON DELETE SET NULL,
  original_size_bytes INTEGER NOT NULL DEFAULT 0,
  result_size_bytes   INTEGER NOT NULL DEFAULT 0,
  result_ratio_pct    INTEGER NOT NULL DEFAULT 0,
  engine              TEXT    NOT NULL DEFAULT '',
  encode_seconds      INTEGER NOT NULL DEFAULT 0,
  finished_at         INTEGER,
  updated_at          INTEGER NOT NULL
);
CREATE INDEX transcode_item_by_state ON transcode_item(state, category_id, media_object_id);
CREATE INDEX transcode_item_by_original ON transcode_item(original_recycle_id) WHERE original_recycle_id IS NOT NULL;

-- How each kind of conversion is doing, so a broken system is not mistaken for broken files.
CREATE TABLE transcode_health (
  kind                 TEXT PRIMARY KEY CHECK (kind IN ('video', 'audio', 'comic')),
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  -- 'ok', 'probing' (trying files further away), 'backoff' (waiting until backoff_until).
  mode                 TEXT    NOT NULL DEFAULT 'ok' CHECK (mode IN ('ok', 'probing', 'backoff')),
  probes_left          INTEGER NOT NULL DEFAULT 0,
  backoff_step         INTEGER NOT NULL DEFAULT 0,
  backoff_until        INTEGER,
  -- The file that failed last: the same file failing again says nothing new about the system.
  last_failed_id       INTEGER NOT NULL DEFAULT 0,
  last_error           TEXT    NOT NULL DEFAULT '',
  run_seq              INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- A file the probe found broken or incomplete, with why; a later good probe or a changed size clears it.
CREATE TABLE file_defect (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  reason          TEXT    NOT NULL CHECK (reason IN ('unreadable', 'empty', 'decode_error')),
  detail          TEXT    NOT NULL DEFAULT '',
  size_bytes      INTEGER NOT NULL,
  detected_at     INTEGER NOT NULL
) WITHOUT ROWID;
