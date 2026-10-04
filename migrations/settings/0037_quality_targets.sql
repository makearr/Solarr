-- Target qualities are named, shared objects (like quality profiles) and a library picks one; what each library had
-- as its own target becomes a target named after the library.
CREATE TABLE quality_target (
  id                 INTEGER PRIMARY KEY,
  name               TEXT    NOT NULL,
  media_type         TEXT    NOT NULL,
  audio_codec        TEXT    NOT NULL DEFAULT '',
  container          TEXT    NOT NULL DEFAULT '',
  bitrate_kbps       INTEGER,
  tolerance_pct      INTEGER NOT NULL DEFAULT 10,
  resolution         TEXT    NOT NULL DEFAULT '',
  video_codec        TEXT    NOT NULL DEFAULT '',
  hdr                TEXT    NOT NULL DEFAULT '',
  audio_channels     TEXT    NOT NULL DEFAULT '',
  audio_languages    TEXT    NOT NULL DEFAULT '',
  subtitle_languages TEXT    NOT NULL DEFAULT '',
  audio_bitrate_kbps INTEGER,
  video_bitrate_kbps INTEGER,
  frame_rate         REAL,
  crf                REAL,
  UNIQUE (media_type, name)
);

INSERT INTO quality_target (name, media_type, audio_codec, container, bitrate_kbps, tolerance_pct, resolution, video_codec, hdr,
                            audio_channels, audio_languages, subtitle_languages, audio_bitrate_kbps, video_bitrate_kbps, frame_rate)
SELECT c.name || ' target', c.media_type, t.audio_codec, t.container, t.bitrate_kbps, t.tolerance_pct, t.resolution, t.video_codec, t.hdr,
       t.audio_channels, t.audio_languages, t.subtitle_languages, t.audio_bitrate_kbps, t.video_bitrate_kbps, t.frame_rate
FROM category_quality_target t JOIN category c ON c.id = t.category_id;

CREATE TABLE category_target (
  category_id INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE,
  target_id   INTEGER NOT NULL REFERENCES quality_target(id)
);

INSERT INTO category_target (category_id, target_id)
SELECT c.id, q.id FROM category c JOIN quality_target q ON q.media_type = c.media_type AND q.name = c.name || ' target'
WHERE EXISTS (SELECT 1 FROM category_quality_target t WHERE t.category_id = c.id);

DROP TABLE category_quality_target;
