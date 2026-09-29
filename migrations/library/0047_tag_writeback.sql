-- Which files have had their MusicBrainz ids written into their tags, once (revision D3).
-- Kept rather than re-read: ffprobe cannot see an MP3's recording id once written.
CREATE TABLE tag_writeback (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  attempted_at    INTEGER NOT NULL,
  -- The keys written, comma-separated; empty when the file already had them all.
  written         TEXT    NOT NULL DEFAULT '',
  error           TEXT
);
