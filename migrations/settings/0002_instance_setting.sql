-- Instance-wide policy the interface may change. config.yml stays bootstrap
-- only: it seeds this on first run and is not consulted again (ADR-21).
CREATE TABLE instance_setting (
  key        TEXT    NOT NULL PRIMARY KEY,
  value_json TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
) WITHOUT ROWID;
