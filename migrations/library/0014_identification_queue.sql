-- F29: the manual identification queue. A file a scan registered but the ADR-24
-- chain (model.RunChain) could not resolve with enough confidence to link
-- automatically -- candidates_json holds the merged ChainResult so the queue can
-- show ranked suggestions rather than just "unknown". A row is removed the moment
-- a later sweep resolves the same media_object_id; nothing here is retained history.

CREATE TABLE identification_queue (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3), same as
  -- library_path.root_folder_id.
  root_folder_id  INTEGER NOT NULL,
  rel_path        TEXT    NOT NULL,
  candidates_json TEXT    NOT NULL,
  first_seen_at   INTEGER NOT NULL,
  last_seen_at    INTEGER NOT NULL
);

-- Sargable for both the category-filtered list (root_folder_id) and the oldest-first
-- cursor (first_seen_at, media_object_id).
CREATE INDEX identification_queue_root ON identification_queue(root_folder_id);
CREATE INDEX identification_queue_cursor ON identification_queue(first_seen_at, media_object_id);
