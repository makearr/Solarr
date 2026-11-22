-- A part a release may carry without owing it: a hidden pregap track is matched when present and never makes the work incomplete.
ALTER TABLE work_part ADD COLUMN optional INTEGER NOT NULL DEFAULT 0 CHECK (optional IN (0, 1));
