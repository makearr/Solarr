-- Every file identification resolved each work from, replacing 0031's one file
-- per work. A real library holds one album across several folders, and a rescan
-- of only the first file's folder left the rest unmatched to their tracks
-- (found 2026-09-17).
CREATE TABLE work_file (
  work_id         INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  first_seen_at   INTEGER NOT NULL,
  PRIMARY KEY (work_id, media_object_id)
) WITHOUT ROWID;
CREATE INDEX work_file_object ON work_file(media_object_id);

INSERT INTO work_file (work_id, media_object_id, first_seen_at)
SELECT work_id, media_object_id, first_seen_at FROM work_file_hint;

DROP TABLE work_file_hint;
