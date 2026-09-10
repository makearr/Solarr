-- Per-library import lists (ADR-38, *Arr-shaped): todo 4.16 scaffolds the
-- screen and the settings rows now; the sync job that actually admits
-- anything is stage 12. type is deliberately a free-form, unvalidated
-- string (confirmed with the user 2026-09-09) -- no music-specific list
-- type is baked in ahead of a real provider integration to name one against.
CREATE TABLE import_list (
  id                 INTEGER PRIMARY KEY,
  category_id        INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  name               TEXT    NOT NULL,
  type               TEXT    NOT NULL,
  url                TEXT    NOT NULL DEFAULT '',
  cadence_hours      INTEGER NOT NULL DEFAULT 24,
  monitor            INTEGER NOT NULL DEFAULT 1 CHECK (monitor IN (0, 1)),
  quality_profile_id INTEGER REFERENCES quality_profile(id),
  -- Free text for now (e.g. "compilation, live, remix"), same shape
  -- SubscriptionFilters' own default exclusions already use -- nothing
  -- reads this yet, since there is no sync job to enforce it against.
  exclusions         TEXT    NOT NULL DEFAULT '',
  enabled            INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  UNIQUE (category_id, name)
);
CREATE INDEX import_list_by_category ON import_list(category_id);
