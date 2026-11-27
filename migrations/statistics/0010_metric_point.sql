-- Solarr's own measured figures over time: one row per series, scope and bucket. Minute rows roll up into hour rows and
-- both are pruned; library.db is correct with this table empty and nothing else reads it.
CREATE TABLE metric_point (
  -- Bucket length in seconds: 60 or 3600.
  resolution INTEGER NOT NULL CHECK (resolution IN (60, 3600)),
  -- Unix seconds of the start of the bucket.
  at         INTEGER NOT NULL,
  -- What is measured (process.cpu_percent, disk.latency_ms, ...).
  series     TEXT    NOT NULL,
  -- What it is measured of: a library id, a root folder path, a provider; empty for the whole process.
  scope      TEXT    NOT NULL,
  value      REAL    NOT NULL,
  PRIMARY KEY (series, scope, resolution, at)
) WITHOUT ROWID;
CREATE INDEX metric_point_by_time ON metric_point(resolution, at);
