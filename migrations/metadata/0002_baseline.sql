-- Every migration up to 0002, squashed into the schema they built (documentation/database-layout.md).
-- migrate:baseline replaces=08b5282869673edd09afa907a9b348b327b130913bbfcb4bf28478cd3b803828

CREATE TABLE metadata_cache (
  kind        TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  source      TEXT    NOT NULL,
  body        TEXT    NOT NULL,
  fetched_at  INTEGER NOT NULL,
  PRIMARY KEY (kind, external_id)
) WITHOUT ROWID;
