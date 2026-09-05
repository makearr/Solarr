-- ADR-53 part 2: bytes that never enter the library again. Keyed on content,
-- never on a filename (Invariant 82) -- a block survives any rename. Exact
-- now (oshash/quick_hash), perceptual later on the same row (chromaprint once
-- stage 7 lands, phash once stage 8 lands), so a block gains reach as
-- fingerprinting ships without the user re-blocking anything. Schema per
-- architecture.md section 5.8.
CREATE TABLE blocked_content (
  id             INTEGER PRIMARY KEY,
  oshash         TEXT,                   -- fast, size+head+tail; the stage-4 identifier
  quick_hash     TEXT,                   -- sampled content hash
  chromaprint    TEXT,                   -- audio, once stage 7 lands (ADR-35)
  phash          TEXT,                   -- video, once stage 8 lands
  size_bytes     INTEGER NOT NULL,
  reason         TEXT    NOT NULL,       -- FAKE | MISLABELLED | CORRUPT | UNWANTED
                                         -- | EXECUTABLE
  note           TEXT,                   -- the user's own words; shown wherever it fires
  media_type     TEXT,                   -- NULL = every media type
  title          TEXT    NOT NULL DEFAULT '',   -- what it claimed to be, for the UI
  release_group  TEXT    NOT NULL DEFAULT '',   -- feeds reputation only when source = user
  source         TEXT    NOT NULL,       -- user | auto
  user_id        INTEGER,
  expires_at     INTEGER,                -- NULL = permanent; manual blocks are permanent
  created_at     INTEGER NOT NULL,
  hit_count      INTEGER NOT NULL DEFAULT 0,
  last_hit_at    INTEGER,
  CHECK (oshash IS NOT NULL OR quick_hash IS NOT NULL
         OR chromaprint IS NOT NULL OR phash IS NOT NULL)
);
CREATE INDEX blocked_by_oshash     ON blocked_content(oshash)      WHERE oshash IS NOT NULL;
CREATE INDEX blocked_by_quick_hash ON blocked_content(quick_hash)  WHERE quick_hash IS NOT NULL;
CREATE INDEX blocked_by_chromaprint ON blocked_content(chromaprint)
  WHERE chromaprint IS NOT NULL;
