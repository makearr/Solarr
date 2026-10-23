-- A copy to another library places the file and keeps the source, recording a further path of the same file
-- (plan 10-libraries L3); a move or rename does not.
ALTER TABLE move_item ADD COLUMN keep_source INTEGER NOT NULL DEFAULT 0 CHECK (keep_source IN (0, 1));
