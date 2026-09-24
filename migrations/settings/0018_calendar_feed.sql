-- A per-user calendar feed token (2026-09-19 review): calendar clients cannot log in, so the
-- token is in the URL. Hashed for lookup, sealed so the link can be shown again; revocable.
CREATE TABLE calendar_feed (
  user_id      INTEGER PRIMARY KEY REFERENCES app_user(id) ON DELETE CASCADE,
  token_hash   TEXT    NOT NULL UNIQUE,
  token_sealed BLOB    NOT NULL,
  created_at   INTEGER NOT NULL
);
