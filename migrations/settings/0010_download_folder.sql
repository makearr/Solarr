-- Download folders (todo 4.18, section 5.1): the folders upstream download
-- clients write to, watched from the Storage section. The registry lands
-- at stage 4; anything that acts on what is sitting there is stage 5.
-- category_id is deliberately nullable -- "optional category hint" -- and
-- has no ON DELETE clause, matching category_root_folder's own convention:
-- deleting a category a download folder still points to is refused by the
-- foreign key, the same as deleting a category a root folder still uses.
CREATE TABLE download_folder (
  id               INTEGER PRIMARY KEY,
  path             TEXT    NOT NULL UNIQUE,
  label            TEXT,
  category_id      INTEGER REFERENCES category(id),
  -- Probed once, at creation, the same as root_folder's own writable
  -- column before the capacity job existed to refresh it -- nothing keeps
  -- this current yet, a named gap, not an oversight.
  readable         INTEGER NOT NULL DEFAULT 1 CHECK (readable IN (0, 1)),
  readable_reason  TEXT
);
