-- Where a scan or import placed each identified file on its album, kept so the file can be aligned
-- to the tracklist enrichment brings later without reading the file again (2026-09-27).
CREATE TABLE file_track_claim (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  disc            INTEGER NOT NULL DEFAULT 0,
  track           INTEGER NOT NULL DEFAULT 0,
  title           TEXT    NOT NULL DEFAULT '',
  recording_mbid  TEXT    NOT NULL DEFAULT ''
);
