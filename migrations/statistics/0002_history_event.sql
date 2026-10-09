-- The jobs.db event a row was copied from (0 for one written here), so the one-time copy of the download history
-- can resume after an interruption and never copies an event twice.
ALTER TABLE history ADD COLUMN event_id INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX history_event ON history(event_id) WHERE event_id > 0;
