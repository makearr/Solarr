-- Files a person identified by hand (the Review page). A scan never identifies them again:
-- a manual choice always wins (answered 2026-09-24). Changed bytes are a new object.
CREATE TABLE manual_identification (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  work_id         INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  decided_at      INTEGER NOT NULL
);
