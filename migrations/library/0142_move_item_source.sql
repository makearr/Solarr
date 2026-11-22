-- A move item carries where its file came from, so a resumed job no longer needs the whole plan kept as one JSON value.
ALTER TABLE move_item ADD COLUMN source_root_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE move_item ADD COLUMN source_rel_path TEXT NOT NULL DEFAULT '';
