-- The anime whose AniList relations have been read, so each is crawled once.
CREATE TABLE work_anime_check (
  work_id    INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  checked_at INTEGER NOT NULL
);
