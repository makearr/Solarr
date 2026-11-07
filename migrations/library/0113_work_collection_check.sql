-- When a provider was last asked which series or lists a work belongs to, by source, so a pass asks each work once in a while.
CREATE TABLE work_collection_check (
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  source     TEXT    NOT NULL,
  checked_at INTEGER NOT NULL,
  PRIMARY KEY (work_id, source)
) WITHOUT ROWID;
