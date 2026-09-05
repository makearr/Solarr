-- ADR-51: one move operation (rename across roots, category migration,
-- rebalance or drain). Resumable; per-file progress is the child table.
-- Schema per architecture.md section 5.8.
CREATE TABLE move_job (
  id              INTEGER PRIMARY KEY,
  reason          TEXT    NOT NULL,       -- RENAME | CATEGORY | REBALANCE | DRAIN
  source_root_id  INTEGER,                -- set for DRAIN
  state           TEXT    NOT NULL,       -- PLANNED | RUNNING | PAUSED | DONE | FAILED
  files_total     INTEGER NOT NULL,
  files_done      INTEGER NOT NULL DEFAULT 0,
  bytes_total     INTEGER NOT NULL,
  bytes_done      INTEGER NOT NULL DEFAULT 0,
  plan_json       TEXT    NOT NULL,       -- the previewed plan, exactly as accepted
  created_at      INTEGER NOT NULL,
  finished_at     INTEGER,
  error           TEXT
);
CREATE INDEX idx_move_job_state ON move_job(state, created_at DESC);

CREATE TABLE move_item (
  move_job_id     INTEGER NOT NULL REFERENCES move_job(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL,
  affinity_key    TEXT    NOT NULL DEFAULT '',
  dest_root_id    INTEGER NOT NULL,
  dest_rel_path   TEXT    NOT NULL,
  bytes           INTEGER NOT NULL,
  state           TEXT    NOT NULL,       -- PENDING | COPIED | VERIFIED | DONE | FAILED
  error           TEXT,
  PRIMARY KEY (move_job_id, media_object_id)
) WITHOUT ROWID;
CREATE INDEX idx_move_item_pending ON move_item(move_job_id, state);
