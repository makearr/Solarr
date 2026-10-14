-- A full sweep that finds a mass of a root's files gone holds the marking back until someone accepts it:
-- a failed mount must not make a library missing (section 10.4). Zero means nothing is held.
ALTER TABLE scan_state ADD COLUMN vanished_held INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_state ADD COLUMN vanished_through INTEGER NOT NULL DEFAULT 0;
