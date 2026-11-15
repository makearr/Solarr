-- The hunt's own "look again at" time, which the planner's backoff must not undo.
ALTER TABLE hunt_queue ADD COLUMN postponed_until INTEGER NOT NULL DEFAULT 0;
