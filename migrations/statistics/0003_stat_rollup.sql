-- The statistics page's figures, computed by a background job from library.db and read only here (section 21.12).
CREATE TABLE stat_rollup (
  metric      TEXT    NOT NULL,
  -- "<category id>|<key>"
  dimension   TEXT    NOT NULL DEFAULT '',
  -- Day bucket; 0 is the current snapshot.
  period      INTEGER NOT NULL,
  value       REAL    NOT NULL,
  count       INTEGER NOT NULL DEFAULT 0,
  computed_at INTEGER NOT NULL,
  PRIMARY KEY (metric, dimension, period)
) WITHOUT ROWID;
