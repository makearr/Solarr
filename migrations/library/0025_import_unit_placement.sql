-- Todo 4.9's placement half. import_unit_file is precisely which media_object
-- rows one unit's own commit produced -- a session-scoped revert must undo
-- exactly what that session created, never a pre-existing work's other files,
-- and media_object carries no back-reference to the unit that placed it.
-- work_created/edition_created record whether ResolveWorksDetailed created the
-- row or matched an existing one: ADR-48 says revert "removes the rows the
-- session created", never one it only linked to, so a revert must know which
-- is which rather than guessing from whether the work looks empty afterward.
CREATE TABLE import_unit_file (
  id              INTEGER PRIMARY KEY,
  unit_id         INTEGER NOT NULL REFERENCES import_unit(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE
);
CREATE INDEX idx_import_unit_file_unit   ON import_unit_file(unit_id);
CREATE INDEX idx_import_unit_file_object ON import_unit_file(media_object_id);

ALTER TABLE import_unit ADD COLUMN work_created    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_unit ADD COLUMN edition_created INTEGER NOT NULL DEFAULT 0;
