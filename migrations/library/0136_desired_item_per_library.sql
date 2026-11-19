-- migrate:disable-foreign-keys
-- A title may be wanted by two libraries: the library joins the unique key (the profile stays in it for two profile targets of one work, F33).
CREATE TABLE "desired_item_new" (
  id                   INTEGER PRIMARY KEY,
  work_id              INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  edition_id           INTEGER REFERENCES edition(id),
  media_ref            TEXT    NOT NULL DEFAULT '',
  media_sort_key       TEXT    NOT NULL DEFAULT '',
  category_id          INTEGER NOT NULL,
  profile_id           INTEGER NOT NULL,
  monitored            INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),

  satisfaction_kind    TEXT    NOT NULL DEFAULT 'SINGLE'
                       CHECK (satisfaction_kind IN ('SINGLE', 'SET')),
  required_count       INTEGER,
  present_count        INTEGER NOT NULL DEFAULT 0,
  state                TEXT    NOT NULL
                       CHECK (state IN ('MISSING', 'PARTIAL', 'COMPLETE', 'UPGRADABLE')),

  available_from       INTEGER,
  available_confidence TEXT,
  available_basis      TEXT,

  dirty                INTEGER NOT NULL DEFAULT 1 CHECK (dirty IN (0, 1)),
  generation           INTEGER NOT NULL DEFAULT 0,
  last_planned_at      INTEGER,
  added_by             INTEGER,
  UNIQUE(work_id, edition_id, media_ref, category_id, profile_id)
);

INSERT INTO desired_item_new (id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id, monitored,
                              satisfaction_kind, required_count, present_count, state, available_from, available_confidence,
                              available_basis, dirty, generation, last_planned_at, added_by)
SELECT id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id, monitored,
       satisfaction_kind, required_count, present_count, state, available_from, available_confidence,
       available_basis, dirty, generation, last_planned_at, added_by
FROM desired_item;

DROP TABLE desired_item;
ALTER TABLE desired_item_new RENAME TO desired_item;

CREATE INDEX desired_dirty ON desired_item(id) WHERE dirty = 1;
CREATE INDEX desired_active ON desired_item(state, work_id) WHERE monitored = 1;
CREATE INDEX desired_by_work ON desired_item(work_id, media_sort_key);
CREATE INDEX desired_by_category ON desired_item(category_id, state);
CREATE INDEX desired_monitored_by_work ON desired_item(work_id, id) WHERE monitored = 1;
CREATE INDEX desired_by_work_category ON desired_item(work_id, category_id);
CREATE INDEX desired_by_edition ON desired_item(edition_id) WHERE edition_id IS NOT NULL;

CREATE TRIGGER desired_item_high_water AFTER DELETE ON desired_item BEGIN
  UPDATE id_high_water SET value = MAX(value, OLD.id) WHERE name = 'desired_item';
END;
