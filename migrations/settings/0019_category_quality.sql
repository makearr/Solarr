-- A library's quality (2026-09-19 review, answered 2026-09-24): the profiles it accepts in
-- priority order, one target, and a cutoff. category.quality_profile_id stays the first of the
-- list, so everything that reads one profile keeps working.
CREATE TABLE category_quality_profile (
  category_id        INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  quality_profile_id INTEGER NOT NULL REFERENCES quality_profile(id),
  ord                INTEGER NOT NULL,
  PRIMARY KEY (category_id, quality_profile_id)
) WITHOUT ROWID;

INSERT INTO category_quality_profile (category_id, quality_profile_id, ord)
SELECT id, quality_profile_id, 0 FROM category WHERE quality_profile_id IS NOT NULL;

-- Upgrades stop once a file is at or above this profile of the list, or meets the target.
ALTER TABLE category ADD COLUMN quality_cutoff_profile_id INTEGER REFERENCES quality_profile(id);

-- The one exact quality a library aims for. The bitrate is a range of +-tolerance_pct around
-- bitrate_kbps, and absent for a lossless codec.
CREATE TABLE category_quality_target (
  category_id   INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE,
  audio_codec   TEXT    NOT NULL,
  container     TEXT    NOT NULL,
  bitrate_kbps  INTEGER,
  tolerance_pct INTEGER NOT NULL DEFAULT 10
);

-- The order a library's list is suggested in until someone chooses one: the user's own example,
-- "Space conscious then lossy 320 then lossless and then anything else".
ALTER TABLE quality_profile ADD COLUMN suggested_rank INTEGER NOT NULL DEFAULT 100;
UPDATE quality_profile SET suggested_rank = 1 WHERE name = 'Space conscious';
UPDATE quality_profile SET suggested_rank = 2 WHERE name = 'Lossy 320';
UPDATE quality_profile SET suggested_rank = 3 WHERE name = 'Lossless preferred';
