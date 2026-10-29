-- What each import list made of each entry it named (ADR-38), so an entry is added once and never again: list_id is
-- settings.db's import_list.id, held without a foreign key as every settings id is (ADR-25).
CREATE TABLE import_list_entry (
  list_id     INTEGER NOT NULL,
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  outcome     TEXT    NOT NULL CHECK (outcome IN ('added', 'held', 'foreign', 'unresolved')),
  work_id     INTEGER NOT NULL DEFAULT 0,
  title       TEXT    NOT NULL DEFAULT '',
  seen_at     INTEGER NOT NULL,
  PRIMARY KEY (list_id, provider, external_id)
);

-- An import list's last preview and last run; previewed_options is the type and settings the preview read.
CREATE TABLE import_list_state (
  list_id           INTEGER PRIMARY KEY,
  previewed_options TEXT    NOT NULL DEFAULT '',
  previewed_at      INTEGER NOT NULL DEFAULT 0,
  run_at            INTEGER NOT NULL DEFAULT 0,
  run_added         INTEGER NOT NULL DEFAULT 0,
  run_failure       TEXT    NOT NULL DEFAULT ''
);
