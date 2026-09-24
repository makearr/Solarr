-- The library a rename ran for, so the move engine can apply its Set Permissions (2026-09-24).
-- A settings.db id: no FK across database files. Null for moves that are not one library's.
ALTER TABLE move_job ADD COLUMN category_id INTEGER;
