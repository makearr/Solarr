-- statistics.db records what was seen and done; nothing that decides what to download, import, replace or delete
-- reads it, so library.db is correct with this file empty (revision 2, D7).

-- What happened to each download, kept for good: jobs.db's event_log is lossy and pruned.
CREATE TABLE history (
  id             INTEGER PRIMARY KEY,
  at             INTEGER NOT NULL,
  -- download.grabbed | download.imported | download.failed | download.removed
  kind           TEXT    NOT NULL,
  -- No foreign keys: the library, the work and the acquisition live in other files; a row outlives them.
  category_id    INTEGER NOT NULL DEFAULT 0,
  work_id        INTEGER NOT NULL DEFAULT 0,
  acquisition_id INTEGER NOT NULL DEFAULT 0,
  release_title  TEXT    NOT NULL DEFAULT '',
  -- The work and its lead as they were named then, shown when the work has gone.
  work_title     TEXT    NOT NULL DEFAULT '',
  lead_name      TEXT    NOT NULL DEFAULT '',
  indexer        TEXT    NOT NULL DEFAULT '',
  client         TEXT    NOT NULL DEFAULT '',
  detail         TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX history_at ON history(at DESC, id DESC);
CREATE INDEX history_work ON history(work_id, at DESC, id DESC);
CREATE INDEX history_category ON history(category_id, at DESC, id DESC);
