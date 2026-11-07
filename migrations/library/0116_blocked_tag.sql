-- A tag an administrator blocked: it is removed from every item and never created again, by a provider or a person.
-- The slug is the tag's own and each of its aliases', so a provider's other spelling stays blocked too.
CREATE TABLE blocked_tag (
  slug       TEXT    PRIMARY KEY,
  name       TEXT    NOT NULL,
  genre_tree TEXT    NOT NULL DEFAULT '',
  blocked_at INTEGER NOT NULL
) WITHOUT ROWID;
