-- settings.db, stage 1 (ADR-25, architecture section 5.5).
--
-- Configuration and credentials, kept apart from library.db so that a library
-- backup, export or support bundle carries no credential in any form
-- (Invariant 40). Small, rarely written, and fully cached in memory.
--
-- Foreign keys inside this file are RESTRICT by default, which is Invariant 41
-- enforced by the database as well as by the application: deleting a
-- configuration entity that is in use is refused, never cascaded. The exception
-- is a user's own rows, which belong to the user rather than referencing them.

-- Local accounts (ADR-14). Multi-user ships at stage 12, but user_id is carried
-- from the first migration so the feature is later work rather than a migration
-- of every historical row.
CREATE TABLE app_user (
  id            INTEGER PRIMARY KEY,
  username      TEXT    NOT NULL UNIQUE,
  -- Null for an account that authenticates by proxy header or API key only.
  password_hash TEXT,
  role          TEXT    NOT NULL CHECK (role IN ('admin', 'operator', 'readonly')),
  created_at    INTEGER NOT NULL,
  -- Which settings this user is shown (ADR-26). Raising the tier reveals
  -- settings; it never changes behaviour.
  ui_tier       TEXT    NOT NULL DEFAULT 'normal'
                        CHECK (ui_tier IN ('normal', 'advanced', 'expert'))
);

-- Where files go, and whether the disk holding them is reachable. Capability
-- probing and free-space accounting arrive with placement (section 11.7).
CREATE TABLE root_folder (
  id     INTEGER PRIMARY KEY,
  path   TEXT    NOT NULL UNIQUE,
  -- Shown in the UI: "music-hdd-1".
  label  TEXT,
  -- An unmounted root counts as unavailable, never as free space (Invariant 20a).
  online INTEGER NOT NULL DEFAULT 1 CHECK (online IN (0, 1))
);

-- The policy boundary for storage, naming, indexers, profiles and visibility
-- (section 19.1). The sidebar lists these, not media types (section 21.1).
CREATE TABLE category (
  id            INTEGER PRIMARY KEY,
  media_type    TEXT    NOT NULL,
  name          TEXT    NOT NULL,
  -- User-ordered rank feeding the composite acquisition priority (ADR-36).
  -- Lower is more important.
  priority_rank INTEGER NOT NULL DEFAULT 100,
  UNIQUE (media_type, name)
);

-- A category spans one or more roots (section 11.7). Ordering and weight serve the
-- placement strategies that need them; accepts_new = 0 drains a root without
-- unmounting it.
CREATE TABLE category_root_folder (
  category_id    INTEGER NOT NULL REFERENCES category(id),
  root_folder_id INTEGER NOT NULL REFERENCES root_folder(id),
  ord            INTEGER NOT NULL DEFAULT 0,
  weight         INTEGER NOT NULL DEFAULT 1,
  accepts_new    INTEGER NOT NULL DEFAULT 1 CHECK (accepts_new IN (0, 1)),
  PRIMARY KEY (category_id, root_folder_id)
) WITHOUT ROWID;

CREATE INDEX category_root_by_root ON category_root_folder(root_folder_id);

-- Per-category visibility (ADR-27). An absent row means no access at all, so a
-- new category is private until granted rather than public until restricted.
CREATE TABLE user_category_access (
  user_id     INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  category_id INTEGER NOT NULL REFERENCES category(id),
  permission  TEXT    NOT NULL
                      CHECK (permission IN ('none', 'read', 'request', 'manage')),
  PRIMARY KEY (user_id, category_id)
) WITHOUT ROWID;

CREATE INDEX user_category_access_by_category ON user_category_access(category_id);

-- Server-side UI state: theme, column layout, saved filters, collapsed
-- sections. Deliberately not browser storage, so it follows the user across
-- devices (ADR-25).
CREATE TABLE user_preference (
  user_id    INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  key        TEXT    NOT NULL,
  value_json TEXT    NOT NULL,
  PRIMARY KEY (user_id, key)
) WITHOUT ROWID;

-- Only the hash is stored, so a settings.db backup cannot be replayed as
-- credentials even by whoever holds the encryption key.
CREATE TABLE api_key (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  name         TEXT    NOT NULL,
  key_hash     TEXT    NOT NULL UNIQUE,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER
);

CREATE INDEX api_key_by_user ON api_key(user_id);
