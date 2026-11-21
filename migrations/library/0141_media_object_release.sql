-- What Solarr knew of a release when it grabbed it, kept with the file it brought: the library file's own name may say none of it.
-- A side table: most media objects (scanned ones) have no row.
CREATE TABLE media_object_release (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  release_title   TEXT    NOT NULL,
  source          TEXT    NOT NULL DEFAULT '',
  release_group   TEXT    NOT NULL DEFAULT '',
  revision        INTEGER NOT NULL DEFAULT 1,
  indexer_id      INTEGER,
  grabbed_at      INTEGER NOT NULL
) WITHOUT ROWID;
