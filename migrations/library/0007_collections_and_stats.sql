-- Collections, the ready queue, artwork, statistics and learned reputation.
-- Completes library.db (section 5.5b).

-- Franchises, box sets, discographies, series, anthologies. Provider-sourced or
-- detected from what is already held.
CREATE TABLE collection (
  id          INTEGER PRIMARY KEY,
  media_type  TEXT    NOT NULL,
  name        TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  source      TEXT    NOT NULL,
  monitored   INTEGER NOT NULL DEFAULT 0 CHECK (monitored IN (0, 1)),
  -- The member count a provider states, when it states one; null means unknown
  -- rather than zero, so "4 of 6" is distinguishable from "4 of unknown".
  complete_at INTEGER,
  added_at    INTEGER NOT NULL
);
CREATE INDEX collection_monitored ON collection(id) WHERE monitored = 1;

CREATE TABLE collection_external_id (
  provider      TEXT    NOT NULL,
  external_id   TEXT    NOT NULL,
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;

CREATE TABLE collection_member (
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  work_id       INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  ord           INTEGER,
  PRIMARY KEY (collection_id, work_id)
) WITHOUT ROWID;
CREATE INDEX collection_member_work ON collection_member(work_id);

-- Decided and pre-flight checked, waiting only for downloader capacity so
-- something dispatches the instant a slot frees (section 10.5).
CREATE TABLE ready_queue (
  candidate_id    INTEGER PRIMARY KEY REFERENCES candidate(id) ON DELETE CASCADE,
  desired_item_id INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  priority        INTEGER NOT NULL,
  queued_at       INTEGER NOT NULL,
  -- Bounded: an entry that waited too long is revalidated rather than dispatched.
  expires_at      INTEGER NOT NULL,
  size_bytes      INTEGER
);
CREATE INDEX ready_queue_order ON ready_queue(priority DESC, queued_at ASC);

-- Computed by a background job, so no chart runs an unbounded aggregate over
-- 500k rows (ADR-28, section 21.12).
CREATE TABLE stat_rollup (
  metric      TEXT    NOT NULL,
  dimension   TEXT    NOT NULL DEFAULT '',
  -- Day bucket; 0 is the current snapshot.
  period      INTEGER NOT NULL,
  value       REAL    NOT NULL,
  count       INTEGER,
  computed_at INTEGER NOT NULL,
  PRIMARY KEY (metric, dimension, period)
) WITHOUT ROWID;
CREATE INDEX stat_rollup_metric ON stat_rollup(metric, period DESC);

-- The records are persistent; the bytes live in cache/images/ and are rebuildable
-- from source_url, so the cache stays disposable (ADR-29).
CREATE TABLE artwork (
  id           INTEGER PRIMARY KEY,
  subject_type TEXT    NOT NULL,
  subject_id   INTEGER NOT NULL,
  kind         TEXT    NOT NULL,
  source_url   TEXT,
  cache_key    TEXT    NOT NULL,
  width        INTEGER,
  height       INTEGER,
  fetched_at   INTEGER,
  UNIQUE(subject_type, subject_id, kind)
);

-- bounded_success is a Wilson lower bound and is what scoring reads; the raw
-- rate is kept for display only, so a group with three lucky imports cannot
-- outrank one with three hundred (ADR-45).
CREATE TABLE group_reputation (
  release_group    TEXT    PRIMARY KEY,
  -- Decay-weighted, not a raw count.
  samples          INTEGER NOT NULL,
  raw_success      REAL,
  bounded_success  REAL,
  corruption_rate  REAL,
  -- Claimed against measured, from ADR-39.
  mislabel_rate    REAL,
  last_observed_at INTEGER,
  updated_at       INTEGER NOT NULL
) WITHOUT ROWID;

-- Kept individually so reputation is recomputable and auditable, and so decay
-- can be reapplied when its half-life changes (ADR-45).
CREATE TABLE group_observation (
  id              INTEGER PRIMARY KEY,
  release_group   TEXT    NOT NULL,
  media_object_id INTEGER REFERENCES media_object(id) ON DELETE SET NULL,
  outcome         TEXT    NOT NULL
                  CHECK (outcome IN ('clean', 'corrupt', 'mislabelled',
                                     'wrong_content', 'unusable')),
  -- Only 'content' counts toward reputation: a full disk is not the group's fault.
  cause           TEXT    NOT NULL CHECK (cause IN ('content', 'environment', 'user')),
  observed_at     INTEGER NOT NULL,
  was_exploration INTEGER NOT NULL DEFAULT 0 CHECK (was_exploration IN (0, 1))
);
CREATE INDEX group_observation_grp ON group_observation(release_group, observed_at DESC);
