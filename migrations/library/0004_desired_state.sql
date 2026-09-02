-- Desired state, what satisfies it, and the materialised aggregates over it (section 5.3)
-- (ADR-25, section 4.3).

CREATE TABLE desired_item (
  id                   INTEGER PRIMARY KEY,
  work_id              INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  edition_id           INTEGER REFERENCES edition(id),
  -- Module-owned and opaque to core: '' for a film, 's03e10' for an episode.
  media_ref            TEXT    NOT NULL DEFAULT '',
  -- Module-supplied and padded, so ordering does not parse the reference.
  media_sort_key       TEXT    NOT NULL DEFAULT '',
  category_id          INTEGER NOT NULL,
  profile_id           INTEGER NOT NULL,
  monitored            INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),

  -- Satisfaction (ADR-23). SET covers a season pack or a tracklist.
  satisfaction_kind    TEXT    NOT NULL DEFAULT 'SINGLE'
                       CHECK (satisfaction_kind IN ('SINGLE', 'SET')),
  required_count       INTEGER,
  present_count        INTEGER NOT NULL DEFAULT 0,
  state                TEXT    NOT NULL
                       CHECK (state IN ('MISSING', 'PARTIAL', 'COMPLETE', 'UPGRADABLE')),

  -- Availability (ADR-23).
  available_from       INTEGER,
  available_confidence TEXT,
  available_basis      TEXT,

  dirty                INTEGER NOT NULL DEFAULT 1 CHECK (dirty IN (0, 1)),
  generation           INTEGER NOT NULL DEFAULT 0,
  last_planned_at      INTEGER,
  added_by             INTEGER,
  UNIQUE(work_id, edition_id, media_ref)
);
-- Partial indexes: the selective sets are tiny against 500k rows (ADR-22).
CREATE INDEX desired_dirty ON desired_item(id) WHERE dirty = 1;
CREATE INDEX desired_active ON desired_item(state, work_id) WHERE monitored = 1;
CREATE INDEX desired_by_work ON desired_item(work_id, media_sort_key);
CREATE INDEX desired_by_category ON desired_item(category_id, state);

-- What actually satisfies a desired item.
CREATE TABLE desired_item_file (
  desired_item_id INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  role            TEXT    NOT NULL DEFAULT 'primary',
  -- Which part of a SET this file covers; '' when the item is SINGLE. In the key,
  -- so one file can cover many parts: an audiobook whose twenty chapters are one
  -- file, or a trade paperback carrying six issues (Appendix E, D1).
  media_ref       TEXT    NOT NULL DEFAULT '',
  added_at        INTEGER NOT NULL,
  PRIMARY KEY (desired_item_id, media_object_id, role, media_ref)
) WITHOUT ROWID;
CREATE INDEX desired_file_by_object ON desired_item_file(media_object_id);
CREATE INDEX desired_file_primary ON desired_item_file(desired_item_id)
  WHERE role = 'primary';

CREATE TABLE request (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL,
  work_id        INTEGER REFERENCES work(id) ON DELETE CASCADE,
  -- Set when the work is not in the library yet.
  external_ref   TEXT,
  media_type     TEXT    NOT NULL,
  category_id    INTEGER,
  scope_json     TEXT,
  state          TEXT    NOT NULL
                 CHECK (state IN ('PENDING', 'APPROVED', 'DECLINED', 'FULFILLED')),
  decided_by     INTEGER,
  decided_at     INTEGER,
  decline_reason TEXT,
  created_at     INTEGER NOT NULL
);
CREATE INDEX request_pending ON request(state, created_at) WHERE state = 'PENDING';
CREATE INDEX request_by_user ON request(user_id, created_at DESC);

-- Materialised, never a query over desired_item (ADR-22).
CREATE TABLE hunt_queue (
  desired_item_id  INTEGER PRIMARY KEY REFERENCES desired_item(id) ON DELETE CASCADE,
  class            TEXT    NOT NULL CHECK (class IN ('missing', 'upgrade')),
  priority         INTEGER NOT NULL,
  last_searched_at INTEGER NOT NULL DEFAULT 0,
  release_date     INTEGER,
  -- Availability and backoff, folded into one comparison.
  eligible_after   INTEGER NOT NULL DEFAULT 0,
  search_count     INTEGER NOT NULL DEFAULT 0
);
-- One covering index per hunt mode (section 9.3).
CREATE INDEX hunt_by_searched ON hunt_queue(class, eligible_after, priority DESC, last_searched_at ASC);
CREATE INDEX hunt_by_release ON hunt_queue(class, eligible_after, priority DESC, release_date ASC);

-- A dashboard count is one lookup whatever the library size (ADR-28). Drift is
-- inevitable, so the reconciliation job is not optional.
CREATE TABLE counter (
  scope      TEXT    NOT NULL,
  metric     TEXT    NOT NULL,
  value      INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (scope, metric)
) WITHOUT ROWID;
