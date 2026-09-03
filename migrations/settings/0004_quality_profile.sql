-- Quality and language profiles (ADR-40, section 19.8). Both are policy, so they live in
-- settings.db. desired_item.profile_id in library.db points at quality_profile.id without a
-- cross-database FK (ADR-25), but category -> profile stays inside settings.db and gets a
-- real FK. Pure ASCII, or sqlc emits wrong SQL that still compiles.
CREATE TABLE quality_profile (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,
  rules_json   TEXT    NOT NULL,   -- hard gates + compare tiers + additive score (ADR-40)
  upgrade_json TEXT    NOT NULL    -- min_delta, min_file_age, max_per_window, window
);

CREATE TABLE language_profile (
  id                      INTEGER PRIMARY KEY,
  name                    TEXT    NOT NULL UNIQUE,
  audio_preferred_json    TEXT    NOT NULL,  -- ordered: ["ja","en"]
  audio_required_json     TEXT,              -- any_of; null/empty = no requirement
  subtitle_preferred_json TEXT,
  subtitle_required_json  TEXT,              -- any_of; null/empty = no requirement
  prefer_original_audio   INTEGER NOT NULL DEFAULT 0 CHECK (prefer_original_audio IN (0, 1)),
  reject_dubbed           INTEGER NOT NULL DEFAULT 0 CHECK (reject_dubbed IN (0, 1))
);

-- A category's default policy (section 19.1, 19.8). Nullable: a category with neither set
-- behaves exactly as it did before this migration.
ALTER TABLE category ADD COLUMN quality_profile_id INTEGER REFERENCES quality_profile(id);
ALTER TABLE category ADD COLUMN language_profile_id INTEGER REFERENCES language_profile(id);
