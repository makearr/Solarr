-- ADR-59's task registry: what a registered periodic action's own scheduler
-- last did. Keyed on the task's own Go-registered key, not an autoincrement
-- id -- there is exactly one row per registered task, upserted after every
-- fire, never accumulated as a log (jobs.db's own job table is already the
-- log of what each fire actually enqueued).
CREATE TABLE schedule_state (
  task_key         TEXT PRIMARY KEY,
  last_run_at      INTEGER,
  last_duration_ms INTEGER,
  -- enqueued / duplicate (ActionKey already matched an active job) / error.
  last_outcome     TEXT CHECK (last_outcome IN ('enqueued', 'duplicate', 'error')),
  last_error       TEXT,
  next_run_at      INTEGER
);
