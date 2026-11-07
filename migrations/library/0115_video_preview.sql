-- Which videos have preview pictures on disk, and made by which algorithm; the pictures themselves are files in the previews folder.
CREATE TABLE video_preview (
  media_object_id   INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  algorithm_version INTEGER NOT NULL,
  has_frame         INTEGER NOT NULL,
  has_sheet         INTEGER NOT NULL,
  made_at           INTEGER NOT NULL
);
