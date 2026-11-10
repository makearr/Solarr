-- A paused task: its scheduled fires are skipped and its jobs stay queued until it is resumed. scope is a task key, or '*' for all.
CREATE TABLE task_pause (
  scope     TEXT    NOT NULL PRIMARY KEY,
  paused_at INTEGER NOT NULL
) WITHOUT ROWID;

-- Which task caused a job, so a pause can find it; absent for work no task started (a person's search, an import).
CREATE TABLE job_task (
  job_id   INTEGER NOT NULL PRIMARY KEY REFERENCES job(id) ON DELETE CASCADE,
  task_key TEXT    NOT NULL
) WITHOUT ROWID;

CREATE INDEX job_task_by_key ON job_task(task_key);
