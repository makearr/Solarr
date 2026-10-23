-- The files that move with a media file but are none themselves: subtitles, nfo documents, pictures beside it or in
-- its folder (2026-10-23). Moved in the same item as their file, never over a file already there.
CREATE TABLE move_item_companion (
  move_job_id     INTEGER NOT NULL REFERENCES move_job(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL,
  source_rel_path TEXT    NOT NULL,
  dest_rel_path   TEXT    NOT NULL,
  PRIMARY KEY (move_job_id, media_object_id, source_rel_path)
) WITHOUT ROWID;
