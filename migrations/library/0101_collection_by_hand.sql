-- A person's rename of a provider's or Solarr's collection, and the works they put in one, which no pass undoes.
ALTER TABLE collection ADD COLUMN name_locked INTEGER NOT NULL DEFAULT 0 CHECK (name_locked IN (0, 1));
ALTER TABLE collection_member ADD COLUMN by_hand INTEGER NOT NULL DEFAULT 0 CHECK (by_hand IN (0, 1));
-- A name a person set by hand on a person, which a provider's refresh leaves alone.
ALTER TABLE person ADD COLUMN name_locked INTEGER NOT NULL DEFAULT 0 CHECK (name_locked IN (0, 1));
