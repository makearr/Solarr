-- The append-only history that shares this file's write profile (section 5.7).
-- Pure ASCII, or sqlc emits wrong SQL that still compiles.

-- Not in library.db: an event is appended after a library commit succeeds, never inside
-- it, because two files cannot commit atomically (ADR-2). Under ADR-3's one writer per
-- file, putting it there would also queue every append behind a library commit, and an
-- event is lossy notification data that must never block one (ADR-9).
CREATE TABLE event_log (
  id             INTEGER PRIMARY KEY,
  at             INTEGER NOT NULL,
  kind           TEXT    NOT NULL,
  subject_type   TEXT,
  subject_id     INTEGER,
  -- No foreign key: app_user lives in settings.db (ADR-25, section 4.3).
  user_id        INTEGER,
  correlation_id TEXT,
  data_json      TEXT    NOT NULL
);
-- "What happened to this album" is the question the history view asks first.
CREATE INDEX event_subject ON event_log(subject_type, subject_id, at DESC);
CREATE INDEX event_at ON event_log(at DESC);
CREATE INDEX event_kind_at ON event_log(kind, at DESC);

-- How far each stream has been pruned, so pruning resumes rather than rescanning.
CREATE TABLE retention_state (
  stream         TEXT    NOT NULL PRIMARY KEY,
  pruned_through INTEGER NOT NULL
) WITHOUT ROWID;
