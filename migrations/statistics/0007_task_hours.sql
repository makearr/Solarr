-- How long each kind of job ran in each hour, for the scheduler's heatmap of the last seven days. Kept by job type
-- rather than by schedule: it is what the workers actually did, including work no schedule asked for.
CREATE TABLE task_hour (
  job_type  TEXT    NOT NULL,
  -- Unix seconds of the start of the hour, in UTC.
  hour_start INTEGER NOT NULL,
  run_ms    INTEGER NOT NULL,
  runs      INTEGER NOT NULL,
  PRIMARY KEY (job_type, hour_start)
) WITHOUT ROWID;

CREATE INDEX task_hour_by_hour ON task_hour(hour_start);
