-- Every migration up to 0005, squashed into the schema they built (documentation/database-layout.md).
-- migrate:baseline replaces=87422040c4638932847b0b4080971436a2f0c2a0c6f7ae0f5463b0277a43f9b7

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
  updated_at INTEGER NOT NULL
, category_id INTEGER, reclaims INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX job_action_active ON job(action_key)
  WHERE action_key IS NOT NULL
    AND state IN ('PENDING', 'PENDING_RETRY', 'LEASED', 'RUNNING');
CREATE INDEX job_dispatch ON job(pool, priority DESC, run_after ASC)
  WHERE state IN ('PENDING', 'PENDING_RETRY');
CREATE INDEX job_reclaim ON job(lease_expires_at)
  WHERE state IN ('LEASED', 'RUNNING');
CREATE INDEX job_by_state ON job(state, updated_at);
CREATE INDEX job_by_category ON job(category_id, updated_at) WHERE category_id IS NOT NULL;

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
CREATE INDEX job_attempt_by_job ON job_attempt(job_id, attempt);

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
CREATE INDEX event_subject ON event_log(subject_type, subject_id, at DESC);
CREATE INDEX event_at ON event_log(at DESC);
CREATE INDEX event_kind_at ON event_log(kind, at DESC);

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
