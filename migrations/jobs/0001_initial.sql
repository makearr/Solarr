-- jobs.db (ADR-2, architecture section 5.6).
--
-- Separate from library.db by write pattern, not by domain: every state
-- transition and every lease renewal is a write, and keeping that churn out of
-- the library WAL is the point. Nothing here is authoritative for library
-- correctness -- if this file is lost, the planner reconstructs the work on the
-- next reconciliation pass.
--
-- ASCII only: a multi-byte character makes sqlc emit wrong SQL that still
-- compiles (documentation/sql-and-sqlc.md).

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
);

-- Invariant 16: every planned action carries a deterministic action_key and the
-- queue deduplicates on it, so a restart cannot enqueue the same download
-- twice. Partial, because a completed job must not block the next one.
CREATE UNIQUE INDEX job_action_active ON job(action_key)
  WHERE action_key IS NOT NULL
    AND state IN ('PENDING', 'PENDING_RETRY', 'LEASED', 'RUNNING');

-- Dispatch reads this and nothing else: one pool, highest priority first,
-- oldest run_after first.
CREATE INDEX job_dispatch ON job(pool, priority DESC, run_after ASC)
  WHERE state IN ('PENDING', 'PENDING_RETRY');

-- The reaper scans only leases that can expire.
CREATE INDEX job_reclaim ON job(lease_expires_at)
  WHERE state IN ('LEASED', 'RUNNING');

CREATE INDEX job_by_state ON job(state, updated_at);

-- One row per attempt, so a job that failed four times can be explained rather
-- than just counted.
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
