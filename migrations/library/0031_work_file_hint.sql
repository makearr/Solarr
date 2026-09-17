-- One file known to belong to each work, recorded where identification resolves a
-- work from a file. Embedded cover art is read from it (2026-09-17). A scan only
-- attaches files to parts once a tracklist exists, so desired_item_file cannot be
-- relied on to name a work's files.
CREATE TABLE work_file_hint (
  work_id         INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  first_seen_at   INTEGER NOT NULL
);
CREATE INDEX work_file_hint_object ON work_file_hint(media_object_id);
