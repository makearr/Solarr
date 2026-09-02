-- Identity: work, edition and the provider data that describes them (section 5.1).
-- Pure ASCII, or sqlc emits wrong SQL that still compiles.

CREATE TABLE work (
  id                INTEGER PRIMARY KEY,
  media_type        TEXT    NOT NULL,
  canonical_title   TEXT    NOT NULL,
  sort_title        TEXT    NOT NULL,
  original_language TEXT,
  year              INTEGER,
  -- Expected runtime, for the pre-download bitrate estimate size/runtime (ADR-40).
  runtime_ms        INTEGER,
  added_at          INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,
  -- No foreign key: app_user lives in settings.db (ADR-25, section 4.3).
  added_by          INTEGER
);
CREATE INDEX work_type_sort ON work(media_type, sort_title);

-- Typed dates, not one year: AvailabilityWindow distinguishes a theatrical
-- release from a digital one, and regions differ (ADR-23).
CREATE TABLE work_date (
  work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  kind    TEXT    NOT NULL,
  -- ISO country; '' is worldwide.
  region  TEXT    NOT NULL DEFAULT '',
  -- Partial dates allowed: 2024, 2024-03, 2024-03-01.
  date    TEXT    NOT NULL,
  source  TEXT    NOT NULL,
  PRIMARY KEY (work_id, kind, region)
) WITHOUT ROWID;

-- Discovery jobs filter and weight across providers, so each keeps its own score (ADR-38).
CREATE TABLE work_rating (
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  provider   TEXT    NOT NULL,
  -- Normalised 0..10.
  score      REAL    NOT NULL,
  votes      INTEGER,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (work_id, provider)
) WITHOUT ROWID;

CREATE TABLE work_certification (
  work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  system  TEXT    NOT NULL,
  rating  TEXT    NOT NULL,
  PRIMARY KEY (work_id, system)
) WITHOUT ROWID;

-- The internal id is permanent; external ids are many and mutable (ADR-8).
CREATE TABLE work_external_id (
  provider      TEXT    NOT NULL,
  external_id   TEXT    NOT NULL,
  work_id       INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  confidence    REAL    NOT NULL DEFAULT 1.0,
  first_seen_at INTEGER NOT NULL,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX work_external_by_work ON work_external_id(work_id);

CREATE TABLE work_title (
  id         INTEGER PRIMARY KEY,
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  title      TEXT    NOT NULL,
  -- Normalised per section 10.2.
  normalized TEXT    NOT NULL,
  language   TEXT,
  kind       TEXT    NOT NULL,
  source     TEXT
);
CREATE INDEX work_title_norm ON work_title(normalized);
CREATE INDEX work_title_work ON work_title(work_id);

-- A part of a work: a track, episode, chapter, issue or disc. media_ref is the
-- module's vocabulary and stays opaque to core (ADR-23).
CREATE TABLE work_part (
  id         INTEGER PRIMARY KEY,
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  -- 's03e10' or 'disc1:track5'.
  media_ref  TEXT    NOT NULL,
  -- Module-supplied and padded, so ordering never parses the reference.
  sort_key   TEXT    NOT NULL DEFAULT '',
  title      TEXT,
  runtime_ms INTEGER,
  -- Partial dates as work_date: an episode air date outlives any intent to hold it.
  date       TEXT,
  -- Per-part monitoring inside a SET: the season, but not s03e05 (Appendix E, D3).
  monitored  INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
  UNIQUE(work_id, media_ref)
);
CREATE INDEX work_part_order ON work_part(work_id, sort_key);
CREATE INDEX work_part_monitored ON work_part(work_id) WHERE monitored = 1;

-- A recording MBID identifies a track, not the release carrying it (ADR-29). part_id
-- is in the key because one recording appears on many albums, and finding those is
-- the cross-album duplicate query.
CREATE TABLE work_part_external_id (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  part_id     INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  confidence  REAL    NOT NULL DEFAULT 1.0,
  PRIMARY KEY (provider, external_id, part_id)
) WITHOUT ROWID;
CREATE INDEX work_part_external_by_part ON work_part_external_id(part_id);

-- Providers disagree about part titles; the disagreement is data (ADR-41).
CREATE TABLE work_part_title (
  id         INTEGER PRIMARY KEY,
  part_id    INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  title      TEXT    NOT NULL,
  normalized TEXT    NOT NULL,
  language   TEXT,
  provider   TEXT
);
CREATE INDEX work_part_title_norm ON work_part_title(normalized);
CREATE INDEX work_part_title_part ON work_part_title(part_id);

-- A file numbered under any known scheme resolves to the right part (ADR-41).
-- The index carries part_id so an absolute-numbering lookup never reads the table.
CREATE TABLE alt_numbering (
  part_id  INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  scheme   TEXT    NOT NULL,
  provider TEXT    NOT NULL,
  alt_ref  TEXT    NOT NULL,
  PRIMARY KEY (part_id, scheme, provider)
) WITHOUT ROWID;
CREATE INDEX alt_numbering_lookup ON alt_numbering(scheme, alt_ref, part_id);

CREATE TABLE edition (
  id               INTEGER PRIMARY KEY,
  work_id          INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  kind             TEXT    NOT NULL,
  label            TEXT    NOT NULL,
  year             INTEGER,
  format           TEXT,
  -- How a label identifies a pressing: "WARP CD 100".
  catalogue_number TEXT,
  -- UPC, EAN or ISBN-13.
  barcode          TEXT,
  -- Overrides work.runtime_ms when set: an extended cut runs longer (ADR-40).
  runtime_ms       INTEGER,
  is_default       INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
  UNIQUE(work_id, kind, label)
);
CREATE INDEX edition_by_work ON edition(work_id);

-- A MusicBrainz Release MBID identifies an edition, a Release Group MBID the
-- work, and ADR-29's identify-from-tags path needs both.
CREATE TABLE edition_external_id (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  edition_id  INTEGER NOT NULL REFERENCES edition(id) ON DELETE CASCADE,
  confidence  REAL    NOT NULL DEFAULT 1.0,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX edition_external_by_edition ON edition_external_id(edition_id);

-- Field-level provider merge; '__manual__' always wins (ADR-8, Invariant 13).
CREATE TABLE work_field (
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  field      TEXT    NOT NULL,
  provider   TEXT    NOT NULL,
  value      TEXT,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (work_id, field, provider)
) WITHOUT ROWID;
