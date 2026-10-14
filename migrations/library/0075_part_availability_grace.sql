-- How long after its time a part is waited for before it is owed, in seconds, stamped from the instance's grace
-- settings by how sure the time is (user, 2026-10-14). NULL takes the built-in grace of its confidence.
ALTER TABLE work_part ADD COLUMN available_grace INTEGER;
