-- A file ffprobe could not read: not asked again until retry_at, or until the file's size changes.
CREATE TABLE media_probe_failure (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  attempts        INTEGER NOT NULL,
  last_error      TEXT    NOT NULL,
  last_attempt_at INTEGER NOT NULL,
  retry_at        INTEGER NOT NULL,
  size_bytes      INTEGER NOT NULL
) WITHOUT ROWID;
