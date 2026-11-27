-- What conversion saved, per library, kind and day; library.db is correct with this table empty and nothing else reads it.
CREATE TABLE transcode_stat (
  -- No foreign key: the library lives in settings.db.
  category_id    INTEGER NOT NULL,
  -- Day bucket (UTC midnight, seconds).
  day            INTEGER NOT NULL,
  kind           TEXT    NOT NULL CHECK (kind IN ('video', 'audio', 'comic', 'ebook')),
  files          INTEGER NOT NULL DEFAULT 0,
  original_bytes INTEGER NOT NULL DEFAULT 0,
  result_bytes   INTEGER NOT NULL DEFAULT 0,
  encode_seconds INTEGER NOT NULL DEFAULT 0,
  failed         INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (category_id, day, kind)
) WITHOUT ROWID;
