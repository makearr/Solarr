-- ADR-48: one inspection-and-import run over one source path. Reversible as a
-- unit, which is what replaces preview-before-commit for automatic imports
-- (the user chose threshold-everywhere over a preview dialog, 2026-09-05).
-- Schema per architecture.md section 5.8. pack_id on import_unit is deliberately
-- not added yet -- the pack table (todo 4.11) does not exist; adding a column
-- referencing it is that block's job, not this one's.
CREATE TABLE import_session (
  id                INTEGER PRIMARY KEY,
  source_path       TEXT    NOT NULL,
  origin            TEXT    NOT NULL,     -- manual | acquisition | watched | adoption
  acquisition_id    INTEGER,              -- set when origin = acquisition
  category_id       INTEGER,              -- target; NULL means "decide per unit". No FK:
                                          -- category lives in settings.db (ADR-25).
  state             TEXT    NOT NULL,     -- INSPECTING | IMPORTING | DONE | FAILED | REVERTED
  threshold         REAL    NOT NULL,     -- the value in force for this run, recorded
  units_total       INTEGER NOT NULL DEFAULT 0,
  units_committed   INTEGER NOT NULL DEFAULT 0,
  units_queued      INTEGER NOT NULL DEFAULT 0,
  bytes_imported    INTEGER NOT NULL DEFAULT 0,
  extract_dir       TEXT,                 -- workspace, removed when the session ends
  started_at        INTEGER NOT NULL,
  finished_at       INTEGER,
  error             TEXT
);
CREATE INDEX idx_import_session_state ON import_session(state, started_at DESC);

-- One discovered unit. Survives the session so the identification queue can show
-- what a queued file belonged to, and so a revert knows what to undo.
CREATE TABLE import_unit (
  id                INTEGER PRIMARY KEY,
  session_id        INTEGER NOT NULL REFERENCES import_session(id) ON DELETE CASCADE,
  rel_path          TEXT    NOT NULL,     -- the unit's own root inside source_path
  shape             TEXT    NOT NULL,     -- SINGLE | SET | PACK | MULTI_DISC
  file_count        INTEGER NOT NULL,
  bytes             INTEGER NOT NULL,
  confidence        REAL    NOT NULL,
  decision          TEXT    NOT NULL,     -- COMMIT | QUEUE | SKIP | FAILED
  work_id           INTEGER REFERENCES work(id),
  edition_id        INTEGER REFERENCES edition(id),
  evidence_json     TEXT    NOT NULL,     -- every signal and what it said (ADR-10 shape)
  candidates_json   TEXT,                 -- ranked alternatives, for the queue
  reason            TEXT                  -- structured code when QUEUE or SKIP
);
CREATE INDEX idx_import_unit_session ON import_unit(session_id, decision);
CREATE INDEX idx_import_unit_queue   ON import_unit(decision, confidence DESC)
  WHERE decision = 'QUEUE';
