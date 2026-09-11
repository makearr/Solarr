-- ADR-50, todo 4.11: one source, many works. Not an identity tier -- a
-- provenance fact (schema reference's own comment). MULTI_DISC is deliberately not a
-- pack kind here: a multi-disc album is already merged into one import_unit
-- at GROUP time (inspect.ShapeMultiDisc), one work and one edition, so it
-- has no "many works" for a pack to relate (private/questions.md). MULTI_SEASON
-- is TV's own kind (stage 8); not reachable from this stage's detector.
CREATE TABLE pack (
  id              INTEGER PRIMARY KEY,
  kind            TEXT    NOT NULL,       -- DISCOGRAPHY | BOX_SET | ANTHOLOGY | COMPILATION
  detected_from   TEXT    NOT NULL,       -- structure | structure_and_name
  confidence      REAL    NOT NULL,
  title           TEXT    NOT NULL DEFAULT '',   -- as released, for display only
  acquisition_id  INTEGER,                -- set when it arrived by acquisition
  session_id      INTEGER REFERENCES import_session(id),
  member_count    INTEGER NOT NULL DEFAULT 0,
  resolved_count  INTEGER NOT NULL DEFAULT 0,    -- members that identified and placed
  created_at      INTEGER NOT NULL
);
CREATE INDEX idx_pack_session ON pack(session_id);

-- One member of a pack. import_unit_id is the source of truth for what this
-- member actually is; work_id/edition_id are denormalised from it once
-- resolved, so a pack's own member list reads without a join back to
-- import_unit for the common case (state = RESOLVED).
CREATE TABLE pack_item (
  pack_id         INTEGER NOT NULL REFERENCES pack(id) ON DELETE CASCADE,
  ord             INTEGER NOT NULL,
  work_id         INTEGER REFERENCES work(id),
  edition_id      INTEGER REFERENCES edition(id),
  import_unit_id  INTEGER REFERENCES import_unit(id),
  state           TEXT    NOT NULL,       -- RESOLVED | QUEUED | FAILED | SKIPPED
  PRIMARY KEY (pack_id, ord)
) WITHOUT ROWID;
CREATE INDEX idx_pack_item_work ON pack_item(work_id);

-- Deliberately not added by migration 0018 (its own comment): the pack table
-- did not exist yet. NULL means "not part of any detected pack" -- the
-- overwhelming common case (a single-release import).
ALTER TABLE import_unit ADD COLUMN pack_id INTEGER REFERENCES pack(id);
CREATE INDEX idx_import_unit_pack ON import_unit(pack_id) WHERE pack_id IS NOT NULL;
