-- One row per search of a wanted item, for the found rate and searches per find (Insights); pruned with the decisions.
CREATE TABLE search_log (
  id              INTEGER PRIMARY KEY,
  at              INTEGER NOT NULL,
  -- No foreign keys: the item and its library live in library.db.
  category_id     INTEGER NOT NULL,
  desired_item_id INTEGER NOT NULL,
  -- 1 when the search found a release the library accepts.
  found           INTEGER NOT NULL CHECK (found IN (0, 1))
);
CREATE INDEX search_log_category_at ON search_log(category_id, at);
CREATE INDEX search_log_at ON search_log(at);
