-- Every migration up to 0010, squashed into the schema and seed rows they built (documentation/database-layout.md).
-- migrate:baseline replaces=fb972824bcade04c6af5709b1c50d92ba1e52c1c943563d9249e48e788a60106

CREATE TABLE job (
  id           INTEGER PRIMARY KEY,
  type         TEXT    NOT NULL,
  -- Which worker pool runs this. Derived from type by a static mapping and
  -- stored so dispatch is one query per pool rather than one per job type.
  pool         TEXT    NOT NULL,
  -- Deterministic dedup key (ADR-6). Null for work that is not idempotent.
  action_key   TEXT,
  priority     INTEGER NOT NULL,
  payload_json TEXT    NOT NULL,
  state        TEXT    NOT NULL CHECK (state IN (
                 'PENDING', 'PENDING_RETRY', 'LEASED', 'RUNNING',
                 'SUCCEEDED', 'FAILED', 'CANCELLED')),
  -- Whether this job changes anything. The dry-run gate is enforced here, at
  -- the job executor, and nowhere else (ADR-20, Invariant 31).
  mutating     INTEGER NOT NULL CHECK (mutating IN (0, 1)),

  attempts     INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  -- Earliest time this may run: backoff, or a scheduled delay.
  run_after    INTEGER NOT NULL DEFAULT 0,

  lease_owner      TEXT,
  lease_expires_at INTEGER,

  -- A job that must not start until another has succeeded.
  depends_on     INTEGER REFERENCES job(id),
  -- Threaded from the API request that caused this, so one identifier
  -- reconstructs a whole causal chain across goroutines (section 18).
  correlation_id TEXT,
  user_id        INTEGER,
  last_error     TEXT,

  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  category_id INTEGER,
  reclaims   INTEGER NOT NULL DEFAULT 0
);
-- A page of one state pages on the index instead of walking the other states' rows.
CREATE INDEX job_by_state_page ON job(state, updated_at DESC, id DESC);

CREATE TABLE job_attempt (
  id          INTEGER PRIMARY KEY,
  job_id      INTEGER NOT NULL REFERENCES job(id) ON DELETE CASCADE,
  attempt     INTEGER NOT NULL,
  lease_owner TEXT    NOT NULL,
  started_at  INTEGER NOT NULL,
  finished_at INTEGER,
  outcome     TEXT,
  error       TEXT
);

CREATE TABLE event_log (
  id             INTEGER PRIMARY KEY,
  at             INTEGER NOT NULL,
  kind           TEXT    NOT NULL,
  subject_type   TEXT,
  subject_id     INTEGER,
  -- No foreign key: app_user lives in settings.db (ADR-25, section 4.3).
  user_id        INTEGER,
  correlation_id TEXT,
  data_json      TEXT    NOT NULL
);
-- Listing events of a few kinds newest first walked the whole log for a rare kind; these indexes lead with the filter and end in the order.
CREATE INDEX event_kind_id ON event_log(kind, id DESC);
CREATE INDEX event_subject_id ON event_log(subject_type, subject_id, id DESC);

CREATE TABLE retention_state (
  stream         TEXT    NOT NULL PRIMARY KEY,
  pruned_through INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE schedule_state (
  task_key         TEXT PRIMARY KEY,
  last_run_at      INTEGER,
  last_duration_ms INTEGER,
  -- enqueued / duplicate (ActionKey already matched an active job) / error.
  last_outcome     TEXT CHECK (last_outcome IN ('enqueued', 'duplicate', 'error')),
  last_error       TEXT,
  next_run_at      INTEGER
);

CREATE UNIQUE INDEX job_action_active ON job(action_key)
  WHERE action_key IS NOT NULL
    AND state IN ('PENDING', 'PENDING_RETRY', 'LEASED', 'RUNNING');
CREATE INDEX job_dispatch ON job(pool, priority DESC, run_after ASC)
  WHERE state IN ('PENDING', 'PENDING_RETRY');
CREATE INDEX job_reclaim ON job(lease_expires_at)
  WHERE state IN ('LEASED', 'RUNNING');
CREATE INDEX job_by_state ON job(state, updated_at);
CREATE INDEX job_by_category ON job(category_id, updated_at) WHERE category_id IS NOT NULL;
CREATE INDEX job_attempt_by_job ON job_attempt(job_id, attempt);
CREATE INDEX event_subject ON event_log(subject_type, subject_id, at DESC);
CREATE INDEX event_at ON event_log(at DESC);
CREATE INDEX event_kind_at ON event_log(kind, at DESC);
CREATE INDEX job_by_dependency ON job(depends_on) WHERE depends_on IS NOT NULL;
CREATE INDEX job_by_updated ON job(updated_at DESC, id DESC);

-- A paused task: its scheduled fires are skipped and its jobs stay queued until it is resumed. scope is a task key, or '*' for all.
CREATE TABLE task_pause (
  scope     TEXT    NOT NULL PRIMARY KEY,
  paused_at INTEGER NOT NULL
) WITHOUT ROWID;

-- Which task caused a job, so a pause can find it; absent for work no task started (a person's search, an import).
CREATE TABLE job_task (
  job_id   INTEGER NOT NULL PRIMARY KEY REFERENCES job(id) ON DELETE CASCADE,
  task_key TEXT    NOT NULL
) WITHOUT ROWID;
CREATE INDEX job_task_by_key ON job_task(task_key);
