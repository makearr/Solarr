-- migrate:disable-foreign-keys
--
-- F33 (todo 4.23): desired_item's own UNIQUE constraint widens from (work_id, edition_id,
-- media_ref) to (work_id, edition_id, media_ref, profile_id), deciding the key shape before
-- stage 5 starts writing to this table heavily -- deciding it after is a migration across the
-- busiest table in the schema (private/plan/08-optional-future.md, F33's own row). Schema only:
-- nothing yet creates a second desired_item for the same work wanting a different profile: the
-- satisfaction check, the upgrade comparison and placement all still assume one, and widening
-- those is F33's own future build-out, not this migration.
--
-- SQLite cannot ALTER a table's own constraint, so this rebuilds the table -- and eight foreign
-- keys across five other tables (desired_item_file, hunt_queue, candidate, decision,
-- arr_import_mapping) reference desired_item(id) with ON DELETE CASCADE or ON DELETE SET NULL, so
-- dropping the old table while foreign key enforcement is on would cascade every one of those rows
-- away. The disable-foreign-keys marker above (store.Migration.DisablesForeignKeys) is what makes
-- this safe: enforcement is off only for this migration's own transaction, PRAGMA foreign_key_check
-- runs before it commits, and every other connection keeps foreign_keys=1 as always (Invariant 4).
-- Every row's id is preserved exactly, so every other table's own foreign key values need no
-- change at all -- only desired_item's own definition and its four indexes are rebuilt.
CREATE TABLE desired_item_new (
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
  UNIQUE(work_id, edition_id, media_ref, profile_id)
);

INSERT INTO desired_item_new (
  id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id, monitored,
  satisfaction_kind, required_count, present_count, state,
  available_from, available_confidence, available_basis,
  dirty, generation, last_planned_at, added_by
)
SELECT
  id, work_id, edition_id, media_ref, media_sort_key, category_id, profile_id, monitored,
  satisfaction_kind, required_count, present_count, state,
  available_from, available_confidence, available_basis,
  dirty, generation, last_planned_at, added_by
FROM desired_item;

DROP TABLE desired_item;
ALTER TABLE desired_item_new RENAME TO desired_item;

CREATE INDEX desired_dirty ON desired_item(id) WHERE dirty = 1;
CREATE INDEX desired_active ON desired_item(state, work_id) WHERE monitored = 1;
CREATE INDEX desired_by_work ON desired_item(work_id, media_sort_key);
CREATE INDEX desired_by_category ON desired_item(category_id, state);
