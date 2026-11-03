-- Every migration up to 0003, squashed into the schema they built (documentation/database-layout.md).
-- migrate:baseline replaces=945cc2b8277498701e8153f57507f44d7ed06a3bd0292b08368b72b62c566857

CREATE TABLE metadata_cache (
  kind        TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  source      TEXT    NOT NULL,
  body        TEXT    NOT NULL,
  fetched_at  INTEGER NOT NULL,
  PRIMARY KEY (kind, external_id)
) WITHOUT ROWID;
CREATE INDEX metadata_cache_by_age ON metadata_cache(fetched_at);
