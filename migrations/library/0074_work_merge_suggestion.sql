-- Two works found to be one (an album held twice: its fitting release is another album's edition), waiting for a
-- person to merge them. One row per work to fold away.
CREATE TABLE work_merge_suggestion (
  work_id      INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  into_work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  found_at     INTEGER NOT NULL
);
CREATE INDEX work_merge_suggestion_into ON work_merge_suggestion(into_work_id);
