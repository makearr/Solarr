-- A target can prefer more quality to less, and ignore what is below it while anything at or above it exists.
ALTER TABLE quality_target ADD COLUMN prefer_over INTEGER NOT NULL DEFAULT 0 CHECK (prefer_over IN (0, 1));
ALTER TABLE quality_target ADD COLUMN ignore_below INTEGER NOT NULL DEFAULT 0 CHECK (ignore_below IN (0, 1));
