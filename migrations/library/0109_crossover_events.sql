-- What an event across series knows beyond its members: the part count the providers give and the key that merges split halves.
CREATE TABLE crossover_event (
  collection_id INTEGER PRIMARY KEY REFERENCES collection(id) ON DELETE CASCADE,
  name_key      TEXT    NOT NULL,
  total_parts   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX crossover_event_by_key ON crossover_event(name_key);

-- A member whose place in the event is a guess from air dates, because its title carries no part number.
CREATE TABLE crossover_member_guess (
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  part_id       INTEGER NOT NULL,
  PRIMARY KEY (collection_id, part_id)
) WITHOUT ROWID;

-- A part of an event a provider lists for a series the library lacks or for an episode it does not hold.
CREATE TABLE crossover_missing_part (
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  number        INTEGER NOT NULL,
  provider      TEXT    NOT NULL,
  external_id   TEXT    NOT NULL,
  series_title  TEXT    NOT NULL,
  season        INTEGER NOT NULL,
  episode       INTEGER NOT NULL,
  title         TEXT    NOT NULL DEFAULT '',
  air_date      TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (collection_id, provider, external_id, season, episode)
) WITHOUT ROWID;

-- Two episodes a person said are not one broadcast, kept by series and episode so a rebuilt part keeps the answer.
CREATE TABLE crossover_not_same (
  low_work_id  INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  low_ref      TEXT    NOT NULL,
  high_work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  high_ref     TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (low_work_id, low_ref, high_work_id, high_ref)
) WITHOUT ROWID;
CREATE INDEX crossover_not_same_by_high ON crossover_not_same(high_work_id);

-- An event a person deleted, so the same title in the same weeks is not found again.
CREATE TABLE crossover_event_dismissed (
  name_key   TEXT NOT NULL,
  first_date TEXT NOT NULL,
  PRIMARY KEY (name_key, first_date)
) WITHOUT ROWID;

-- A family of series whose provider episode lists were compared for events, so it is not fetched again for a month.
CREATE TABLE crossover_family_checked (
  family_key TEXT PRIMARY KEY,
  checked_at INTEGER NOT NULL
) WITHOUT ROWID;
