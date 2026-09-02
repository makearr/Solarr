-- Per-root sweep progress (section 10.4). A scan across 500k files is a job with
-- persisted progress, not a function call: a restart continues from the last
-- committed batch (ADR-3).

CREATE TABLE scan_state (
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3).
  root_folder_id INTEGER NOT NULL PRIMARY KEY,
  -- The wall-clock time the last completed sweep started, as Unix seconds. The
  -- start, never the end: a directory written after the walk had passed it would
  -- otherwise sit behind the watermark and never be read again
  -- (documentation/database-layout.md). Zero means nothing has completed yet.
  swept_through  INTEGER NOT NULL DEFAULT 0,
  -- The last directory committed by an interrupted sweep, relative to the root.
  -- Empty means there is nothing to resume. Compared segment by segment, never as
  -- a string: walk order puts 'a/c' before 'a-b'.
  cursor         TEXT    NOT NULL DEFAULT '',
  -- When the sweep in progress began, and when the last one finished, so an
  -- operator can see a scan that has stalled.
  started_at     INTEGER,
  finished_at    INTEGER,
  -- Counters from the last completed sweep, for the same reason.
  files_found    INTEGER NOT NULL DEFAULT 0,
  directories_read INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
