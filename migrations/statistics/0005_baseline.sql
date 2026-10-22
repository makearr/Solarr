-- Every migration up to 0005, squashed into the schema they built (documentation/database-layout.md).
-- migrate:baseline replaces=dde867bd35998135c8423cbbc23e6ccf5774679dc5d693143fedd87a3067d802

CREATE TABLE history (
  id             INTEGER PRIMARY KEY,
  at             INTEGER NOT NULL,
  -- download.grabbed | download.imported | download.failed | download.removed
  kind           TEXT    NOT NULL,
  -- No foreign keys: the library, the work and the acquisition live in other files; a row outlives them.
  category_id    INTEGER NOT NULL DEFAULT 0,
  work_id        INTEGER NOT NULL DEFAULT 0,
  acquisition_id INTEGER NOT NULL DEFAULT 0,
  release_title  TEXT    NOT NULL DEFAULT '',
  -- The work and its lead as they were named then, shown when the work has gone.
  work_title     TEXT    NOT NULL DEFAULT '',
  lead_name      TEXT    NOT NULL DEFAULT '',
  indexer        TEXT    NOT NULL DEFAULT '',
  client         TEXT    NOT NULL DEFAULT '',
  detail         TEXT    NOT NULL DEFAULT ''
, event_id INTEGER NOT NULL DEFAULT 0);
CREATE INDEX history_at ON history(at DESC, id DESC);
CREATE INDEX history_work ON history(work_id, at DESC, id DESC);
CREATE INDEX history_category ON history(category_id, at DESC, id DESC);
CREATE UNIQUE INDEX history_event ON history(event_id) WHERE event_id > 0;

CREATE TABLE stat_rollup (
  metric      TEXT    NOT NULL,
  -- "<category id>|<key>"
  dimension   TEXT    NOT NULL DEFAULT '',
  -- Day bucket; 0 is the current snapshot.
  period      INTEGER NOT NULL,
  value       REAL    NOT NULL,
  count       INTEGER NOT NULL DEFAULT 0,
  computed_at INTEGER NOT NULL,
  PRIMARY KEY (metric, dimension, period)
) WITHOUT ROWID;

CREATE TABLE candidate (
  id               INTEGER PRIMARY KEY,
  indexer_id       INTEGER NOT NULL,
  guid             TEXT    NOT NULL,
  title            TEXT    NOT NULL,
  attributes_id    INTEGER,
  size_bytes       INTEGER,
  seeders          INTEGER,
  leechers         INTEGER,
  published_at     INTEGER,
  protocol         TEXT    NOT NULL,
  download_url     TEXT    NOT NULL,
  parse_confidence REAL,
  source           TEXT    NOT NULL,
  first_seen_at    INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL,
  UNIQUE(indexer_id, guid)
);
CREATE INDEX candidate_expiry ON candidate(expires_at);

CREATE TABLE candidate_decision (
  id               INTEGER PRIMARY KEY,
  candidate_id     INTEGER NOT NULL,
  desired_item_id  INTEGER NOT NULL,
  -- The library the item belonged to when it was decided, so the statistics count decisions without library.db.
  category_id      INTEGER NOT NULL DEFAULT 0,
  outcome          TEXT    NOT NULL CHECK (outcome IN ('ACCEPT', 'REJECT', 'DEFER')),
  score            REAL    NOT NULL,
  components_json  TEXT    NOT NULL,
  rejections_json  TEXT,
  deferrals_json   TEXT,
  match_confidence REAL,
  decided_at       INTEGER NOT NULL
);
CREATE INDEX decision_by_item ON candidate_decision(desired_item_id, decided_at DESC);
CREATE INDEX decision_by_candidate ON candidate_decision(candidate_id);
CREATE INDEX decision_by_category_at ON candidate_decision(category_id, decided_at);

CREATE TABLE decision_rollup (
  desired_item_id INTEGER NOT NULL,
  period          INTEGER NOT NULL,
  outcome         TEXT    NOT NULL,
  count           INTEGER NOT NULL,
  best_score      REAL,
  top_reason      TEXT,
  PRIMARY KEY (desired_item_id, period, outcome)
) WITHOUT ROWID;
CREATE INDEX decision_rollup_period ON decision_rollup(period, outcome);

CREATE TABLE search_log (
  id              INTEGER PRIMARY KEY,
  at              INTEGER NOT NULL,
  -- No foreign keys: the item and its library live in library.db.
  category_id     INTEGER NOT NULL,
  desired_item_id INTEGER NOT NULL,
  -- 1 when the search found a release the library accepts.
  found           INTEGER NOT NULL CHECK (found IN (0, 1))
);
CREATE INDEX search_log_category_at ON search_log(category_id, at);
CREATE INDEX search_log_at ON search_log(at);
