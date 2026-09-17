-- Every identification run for a file that its embedded MBIDs did not settle: each pass,
-- what it concluded and the candidates it weighed, so a manual decision can show why.
-- Bounded to the newest few runs per file by the writer.
CREATE TABLE identification_attempt (
  id              INTEGER PRIMARY KEY,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  attempted_at    INTEGER NOT NULL,
  outcome         TEXT    NOT NULL,
  passes_json     TEXT    NOT NULL
);
CREATE INDEX identification_attempt_object ON identification_attempt(media_object_id, attempted_at, id);
