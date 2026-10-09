-- The releases searches found and what was decided about each (section 12's "why wasn't this downloaded"), moved
-- from library.db (revision 2, D7). Ids are kept from library.db, so a held release's or an acquisition's
-- candidate_id and a decision id a person sends by still name the same row. No foreign keys: the wanted item lives in
-- library.db, and a decision outlives it until retention ages it out.
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

-- Aged decisions, counted per item, day and outcome once the detail is gone.
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
