-- A vanished path is MISSING, not deleted, until the root is confirmed online and a
-- full sweep has looked for it (section 10.4). This gives library_path the two
-- columns that track it: when a path was last confirmed present, and when it was
-- first noticed absent.

ALTER TABLE library_path ADD COLUMN last_seen_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE library_path ADD COLUMN missing_since INTEGER;

-- Sargable for the mark-missing pass: paths in one root, not already missing, whose
-- last observation predates the current full sweep's start.
CREATE INDEX library_path_missing_scan ON library_path(root_folder_id, missing_since, last_seen_at);
