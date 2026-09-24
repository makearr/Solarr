-- When each artist's metadata was last refreshed, so the scheduled refresh takes the stalest first
-- (answered 2026-09-24).
CREATE TABLE person_refresh (
  person_id    INTEGER PRIMARY KEY REFERENCES person(id) ON DELETE CASCADE,
  refreshed_at INTEGER NOT NULL
);
