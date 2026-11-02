-- One perceptual hash per sampled frame of a video (ADR-35): media_object.phash, of the mean frame, finds candidates
-- and these decide. frames is 9 bytes per informative frame, its sample slot then the hash big-endian, in slot order.
CREATE TABLE video_frame_hash (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  frames          BLOB    NOT NULL
);
