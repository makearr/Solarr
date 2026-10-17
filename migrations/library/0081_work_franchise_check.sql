-- The series a franchise lookup has been made for, found in one or not, so a pass asks about each once.
CREATE TABLE work_franchise_check (
  work_id    INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  checked_at INTEGER NOT NULL
);
