-- Todo 4.21's feedback half: the COMMIT/QUEUE breakdown already recomputed by
-- RecomputeImportSessionCounts gains its missing SKIP/FAILED counterparts, so
-- the Import screen and its SSE progress events can show all four without a
-- live COUNT. Appended after the existing columns, as ALTER TABLE ADD COLUMN
-- always does -- every query column list is updated to match.
ALTER TABLE import_session ADD COLUMN units_skipped INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_session ADD COLUMN units_failed  INTEGER NOT NULL DEFAULT 0;
