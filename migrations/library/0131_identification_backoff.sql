-- A file the chain keeps failing to place is asked about less and less often: tries counts repeats with the same answer for
-- the same bytes, next_try_at is when the daily retry may pick it up again, tried_size is the size that answer was for.
ALTER TABLE identification_queue ADD COLUMN tries INTEGER NOT NULL DEFAULT 0;
ALTER TABLE identification_queue ADD COLUMN next_try_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE identification_queue ADD COLUMN tried_size INTEGER NOT NULL DEFAULT 0;
CREATE INDEX identification_queue_due ON identification_queue(root_folder_id, next_try_at);
