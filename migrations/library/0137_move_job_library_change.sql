-- What a library move changed besides the files, so a move job that moved no file can put it back.
CREATE TABLE move_job_library_change (
  move_job_id INTEGER PRIMARY KEY REFERENCES move_job(id) ON DELETE CASCADE,
  change_json TEXT    NOT NULL
);
