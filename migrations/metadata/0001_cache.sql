-- cache/metadata.db (ADR-2): disposable, safe to delete at any time. Nothing here is
-- authoritative -- external ids and identity live in library.db, so losing this file
-- costs re-fetching detail, never identity. No backup rotation is wired to it for
-- exactly that reason.

-- One row per (kind, external_id): an entity's answer, normalised to this project's
-- own JSON shape before it is cached, never a provider's wire format -- so a source
-- falling over to another one (ADR-29) reads the same cached row regardless of which
-- one last answered, instead of missing the cache on every failover. "source" is who
-- last answered, kept for observability only -- it is not part of the key.
CREATE TABLE metadata_cache (
  kind        TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  source      TEXT    NOT NULL,
  body        TEXT    NOT NULL,
  fetched_at  INTEGER NOT NULL,
  PRIMARY KEY (kind, external_id)
) WITHOUT ROWID;
