-- migrate:disable-foreign-keys
-- The operator role is called user, and a library grant can be add (a request that is approved at once). SQLite cannot
-- widen a CHECK, so both tables are rebuilt; a user may also be kept from the items carrying a tag.
CREATE TABLE app_user_new (
  id            INTEGER PRIMARY KEY,
  username      TEXT    NOT NULL UNIQUE,
  password_hash TEXT,
  role          TEXT    NOT NULL CHECK (role IN ('admin', 'user', 'readonly')),
  created_at    INTEGER NOT NULL,
  ui_tier       TEXT    NOT NULL DEFAULT 'normal'
                        CHECK (ui_tier IN ('normal', 'advanced', 'expert')),
  ui_tier_default TEXT NOT NULL DEFAULT 'normal'
  CHECK (ui_tier_default IN ('normal', 'advanced', 'expert')),
  ui_tier_max TEXT NOT NULL DEFAULT 'expert'
  CHECK (ui_tier_max IN ('normal', 'advanced', 'expert')),
  new_library_permission TEXT NOT NULL DEFAULT 'none'
  CHECK (new_library_permission IN ('none', 'read', 'request', 'add', 'manage')),
  auto_approve INTEGER NOT NULL DEFAULT 0 CHECK (auto_approve IN (0, 1))
);
INSERT INTO app_user_new (id, username, password_hash, role, created_at, ui_tier, ui_tier_default, ui_tier_max, new_library_permission, auto_approve)
SELECT id, username, password_hash, CASE role WHEN 'operator' THEN 'user' ELSE role END, created_at, ui_tier, ui_tier_default, ui_tier_max,
       new_library_permission, auto_approve
FROM app_user;
DROP TABLE app_user;
ALTER TABLE app_user_new RENAME TO app_user;

CREATE TABLE user_category_access_new (
  user_id     INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  category_id INTEGER NOT NULL REFERENCES category(id),
  permission  TEXT    NOT NULL
                      CHECK (permission IN ('none', 'read', 'request', 'add', 'manage')),
  PRIMARY KEY (user_id, category_id)
) WITHOUT ROWID;
INSERT INTO user_category_access_new (user_id, category_id, permission) SELECT user_id, category_id, permission FROM user_category_access;
DROP TABLE user_category_access;
ALTER TABLE user_category_access_new RENAME TO user_category_access;
CREATE INDEX user_category_access_by_category ON user_category_access(category_id);

-- tag_id is a library.db tag, referenced by id with no foreign key.
CREATE TABLE user_blocked_tag (
  user_id  INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  tag_id   INTEGER NOT NULL,
  tag_name TEXT    NOT NULL,
  PRIMARY KEY (user_id, tag_id)
) WITHOUT ROWID;
