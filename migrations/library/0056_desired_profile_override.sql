-- One album's own quality profile, replacing its library's profile list for search and upgrades.
-- No FK: the profile lives in settings.db (ADR-25).
CREATE TABLE desired_profile_override (
  desired_item_id INTEGER PRIMARY KEY REFERENCES desired_item(id) ON DELETE CASCADE,
  profile_id      INTEGER NOT NULL
) WITHOUT ROWID;
