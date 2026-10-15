-- A stalled download whose finished files are being imported: once that import ends, the rest is searched again.
ALTER TABLE acquisition_stall ADD COLUMN importing_finished INTEGER NOT NULL DEFAULT 0 CHECK (importing_finished IN (0, 1));
