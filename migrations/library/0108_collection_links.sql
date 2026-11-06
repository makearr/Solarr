-- Why two collections of different libraries are shown as one, and what a person undid so it is not made again.
CREATE TABLE collection_link_origin (
  low_id     INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  high_id    INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  reason     TEXT    NOT NULL,
  detail     TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (low_id, high_id)
) WITHOUT ROWID;
CREATE INDEX collection_link_origin_high ON collection_link_origin(high_id);

-- Keys are "provider:id" of a collection's franchise ids, so the memory outlives a collection Solarr rebuilds.
CREATE TABLE collection_link_refusal (
  low_key  TEXT NOT NULL,
  high_key TEXT NOT NULL,
  PRIMARY KEY (low_key, high_key)
) WITHOUT ROWID;

-- A franchise a provider says a collection's works belong to, beyond the ids the collection itself carries.
CREATE TABLE collection_declared_key (
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  provider      TEXT    NOT NULL,
  external_id   TEXT    NOT NULL,
  PRIMARY KEY (collection_id, provider, external_id)
) WITHOUT ROWID;
CREATE INDEX collection_declared_key_by_key ON collection_declared_key(provider, external_id);

-- Why a work joined a collection Solarr made (a crossover episode it shares, a title, a provider), shown to the person who may undo it.
CREATE TABLE collection_member_origin (
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  work_id       INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  reason        TEXT    NOT NULL,
  detail        TEXT    NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (collection_id, work_id)
) WITHOUT ROWID;
CREATE INDEX collection_member_origin_by_work ON collection_member_origin(work_id);
