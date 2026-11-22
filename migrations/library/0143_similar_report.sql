-- The similar-videos report is computed by a job and stored per level, so the screen reads it instead of comparing every hash in the request.
CREATE TABLE similar_report (
  level       TEXT    NOT NULL PRIMARY KEY,
  computed_at INTEGER NOT NULL,
  groups_json TEXT    NOT NULL
) WITHOUT ROWID;
