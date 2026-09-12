-- Indexers (ADR-37, 43, 47): Newznab/Torznab search providers, stage 5's own first
-- schema piece. One row per configured connection. api_key is sealed by secrets.Cipher,
-- the same shape arr_instance (0011) already uses -- nullable here, unlike arr_instance's
-- own NOT NULL key, since a public Torznab endpoint may need none at all.
CREATE TABLE indexer (
  id              INTEGER PRIMARY KEY,
  name            TEXT    NOT NULL UNIQUE,
  impl            TEXT    NOT NULL,      -- "newznab" | "torznab" (ADR-43); no CHECK here,
                                          -- the same open-ended shape arr_instance's own
                                          -- app column started before every value existed
  base_url        TEXT    NOT NULL,
  api_key         BLOB,
  enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  priority        INTEGER NOT NULL DEFAULT 50,
  -- Populated from the indexer's own t=caps response, never assumed (ADR-43).
  categories_json TEXT,
  caps_fetched_at INTEGER,
  -- egress = {proxy_url, flaresolverr_url} (ADR-47), expert tier -- unused until that
  -- pass wires it, present now so it needs no further migration when it does.
  settings_json   TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
