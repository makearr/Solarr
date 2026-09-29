-- When an item entered the hunt queue (M1c): ADR-36's aging counts from here, not from its last search.
ALTER TABLE hunt_queue ADD COLUMN queued_since INTEGER NOT NULL DEFAULT 0;
