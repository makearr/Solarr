-- A series the user deleted and asked not to see again: a scan, a follow or an import list leaves it out.
CREATE TABLE series_exclusion (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  title       TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
