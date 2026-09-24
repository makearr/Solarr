-- Files a person marked "not media" from the identification queue (F29's last verb, 2026-09-24).
-- Keyed on the media object, so these bytes stay out of the queue on every later scan; changed
-- bytes are a new object and are judged afresh. Never a filename (ADR-53).
CREATE TABLE not_media (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  marked_at       INTEGER NOT NULL
);
