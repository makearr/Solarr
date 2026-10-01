-- A failed download caps the item's search backoff at a day (2026-09-30 decision): the next try is not a week away.
ALTER TABLE hunt_queue ADD COLUMN after_failure INTEGER NOT NULL DEFAULT 0;
