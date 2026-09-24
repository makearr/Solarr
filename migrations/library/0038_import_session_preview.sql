-- A resumed import keeps what it was started with (answered 2026-09-24): the threshold was
-- recorded, preview was not.
ALTER TABLE import_session ADD COLUMN preview INTEGER NOT NULL DEFAULT 0 CHECK (preview IN (0, 1));
