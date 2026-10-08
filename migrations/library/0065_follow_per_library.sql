-- migrate:disable-foreign-keys
--
-- One follow per subject, user and library: a music artist who also acts can be followed in a music and a TV
-- library at once. A rebuild, since SQLite cannot change a UNIQUE constraint; arr_import_item references
-- subscription(id), so foreign keys are off for this migration only and every id is kept.
CREATE TABLE subscription_new (
  id                     INTEGER PRIMARY KEY,
  subject_type           TEXT    NOT NULL
                         CHECK (subject_type IN ('person', 'organisation', 'tag')),
  subject_id             INTEGER NOT NULL,
  media_types_json       TEXT    NOT NULL,
  roles_json             TEXT,
  category_id            INTEGER,
  profile_id             INTEGER,
  filters_json           TEXT,
  monitor_back_catalogue INTEGER NOT NULL DEFAULT 0
                         CHECK (monitor_back_catalogue IN (0, 1)),
  enabled                INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  user_id                INTEGER,
  created_at             INTEGER NOT NULL,
  UNIQUE(subject_type, subject_id, user_id, category_id)
);
INSERT INTO subscription_new (id, subject_type, subject_id, media_types_json, roles_json, category_id, profile_id,
                              filters_json, monitor_back_catalogue, enabled, user_id, created_at)
SELECT id, subject_type, subject_id, media_types_json, roles_json, category_id, profile_id,
       filters_json, monitor_back_catalogue, enabled, user_id, created_at
FROM subscription;
DROP TABLE subscription;
ALTER TABLE subscription_new RENAME TO subscription;
CREATE INDEX subscription_subject ON subscription(subject_type, subject_id) WHERE enabled = 1;
