-- *Arr import runs (todo 4.13, ADR-52): one row per apply/park run, and one
-- row per entity it touched, the revert-tracking shape import_session/
-- import_unit already use for a normal import (work_created/*_created
-- booleans distinguish "this run created it" from "this run only matched an
-- existing row", so a revert never deletes something another run or a scan
-- already owns). instance_id names a settings.db arr_instance row with no FK
-- -- cross-database references are composed in Go, never joined (ADR-25).
CREATE TABLE arr_import (
  id               INTEGER PRIMARY KEY,
  instance_id      INTEGER NOT NULL,
  app              TEXT    NOT NULL,
  state            TEXT    NOT NULL,     -- RUNNING | DONE | FAILED | REVERTED
  started_at       INTEGER NOT NULL,
  finished_at      INTEGER,
  error            TEXT,
  artists_created  INTEGER NOT NULL DEFAULT 0,
  artists_matched  INTEGER NOT NULL DEFAULT 0,
  albums_created   INTEGER NOT NULL DEFAULT 0,
  albums_matched   INTEGER NOT NULL DEFAULT 0,
  items_parked     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_arr_import_instance ON arr_import(instance_id);

-- One imported or parked entity. kind = 'parked' is every app but Lidarr
-- (todo 4.13: "connect, preview, park") -- payload_json carries its own
-- source payload verbatim, expanded into real rows the moment that media
-- type ships (ADR-52), and person_id/work_id/etc stay NULL for it.
-- Every reference below is ON DELETE SET NULL, not CASCADE: ArrImportRevert
-- deletes the underlying person/work/subscription/desired_item it created,
-- but keeps this row itself as the run's own historical record (the same
-- "clear placement, keep the row" shape import_unit's work_id/edition_id
-- already have after library.ImportRevert clears them) -- a hard REFERENCES
-- with no ON DELETE clause would instead block the very delete Revert exists
-- to make.
CREATE TABLE arr_import_item (
  id                    INTEGER PRIMARY KEY,
  import_id             INTEGER NOT NULL REFERENCES arr_import(id) ON DELETE CASCADE,
  kind                  TEXT    NOT NULL,     -- artist | album | parked
  source_id             TEXT    NOT NULL,     -- the *Arr app's own id for this entity
  title                 TEXT    NOT NULL,
  person_id             INTEGER REFERENCES person(id) ON DELETE SET NULL,
  person_created        INTEGER NOT NULL DEFAULT 0 CHECK (person_created IN (0, 1)),
  subscription_id       INTEGER REFERENCES subscription(id) ON DELETE SET NULL,
  work_id               INTEGER REFERENCES work(id) ON DELETE SET NULL,
  work_created          INTEGER NOT NULL DEFAULT 0 CHECK (work_created IN (0, 1)),
  desired_item_id       INTEGER REFERENCES desired_item(id) ON DELETE SET NULL,
  desired_item_created  INTEGER NOT NULL DEFAULT 0 CHECK (desired_item_created IN (0, 1)),
  payload_json          TEXT,
  created_at            INTEGER NOT NULL
);
CREATE INDEX idx_arr_import_item_import ON arr_import_item(import_id);
