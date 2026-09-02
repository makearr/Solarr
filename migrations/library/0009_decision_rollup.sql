-- Aged decision detail, beside the decisions it aggregates (section 5.7).
-- Pure ASCII, or sqlc emits wrong SQL that still compiles.

-- Decision retention is full detail for 90 days and aggregates thereafter (Part VI Q5),
-- and at 500k items with active hunting this is the fastest-growing data in the system.
-- It stays in library.db so F1's "why was this not downloaded" reads the recent and the
-- aged record in one query rather than merging across two files.
CREATE TABLE decision_rollup (
  desired_item_id INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  -- Day bucket, as a Unix day number.
  period          INTEGER NOT NULL,
  outcome         TEXT    NOT NULL,
  count           INTEGER NOT NULL,
  best_score      REAL,
  -- The rejection rule that fired most often in the bucket.
  top_reason      TEXT,
  PRIMARY KEY (desired_item_id, period, outcome)
) WITHOUT ROWID;
-- "What has been happening lately", across items rather than within one.
CREATE INDEX decision_rollup_period ON decision_rollup(period, outcome);
