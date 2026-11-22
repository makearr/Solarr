-- Every migration up to 0004, squashed into the schema and seed rows they built (documentation/database-layout.md).
-- migrate:baseline replaces=bcecbdcfcca9c337825bc5b3f12effea3692826694ff63b717146c35caf932b8

CREATE TABLE metadata_cache (
  kind        TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  source      TEXT    NOT NULL,
  body        TEXT    NOT NULL,
  fetched_at  INTEGER NOT NULL,
  body_bytes  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (kind, external_id)
) WITHOUT ROWID;
CREATE INDEX metadata_cache_by_age ON metadata_cache(fetched_at, body_bytes);

-- IMDb's daily ratings dataset (metadata-providers: IMDb datasets); disposable like the rest of metadata.db.
CREATE TABLE imdb_rating (
  tconst TEXT    NOT NULL PRIMARY KEY,
  rating REAL    NOT NULL,
  votes  INTEGER NOT NULL
) WITHOUT ROWID;
